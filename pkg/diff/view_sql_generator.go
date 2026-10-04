package diff

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/google/go-cmp/cmp"
	"github.com/stripe/pg-schema-diff/internal/schema"
)

type viewDiff struct {
	oldAndNew[schema.View]
	privilegesDiff listDiff[schema.TablePrivilege, privilegeDiff]
	// stableTableDependencies names the tables the new definition reads whose read columns this
	// plan neither adds nor changes. An in-place replacement does not have to wait for them.
	stableTableDependencies map[string]bool
}

func buildViewDiff(
	deletedTablesByName map[string]schema.Table,
	tableDiffsByName map[string]tableDiff,
	old, new schema.View) (viewDiff, bool, error) {
	// Assuming the view's outputted columns do not change, there are few situations where the view
	// needs to be totally recreated (delete then re-add):
	//- One of its dependent columns is deleted then added. As in, a column that it depends on in the old and new is recreated.
	//- Same as above but for the table itself.
	//- "Outputted" columns of the view change (remove or type altered)
	//
	// It does not need to be recreated in the following situations:
	// - The recreated column/table is only just becoming a dependency: In this case, it can rely on being altered.
	// --> A column "foobar" is added to the table, and "foobar" is being added to the view.
	// - The recreated column/table is no longer a dependency: In this case, it can rely on being altered.
	// --> A column "foobar" is removed to the table, and "foobar" is being removed from the view.
	//
	// For now, we will go with the simplest behavior and always recreate the view if a dependent column/table,
	// and that column/table is deleted/recreated. In part, this is because we cannot depend on individual column
	// changes...all added and removes columns are combined into the same SQL vertex.
	// - See https://github.com/stripe/pg-schema-diff/issues/135#issuecomment-2357382217 for details.
	// - For some table X, it is currently not possible to create a SQL statement outside the table sql generator
	// that comes before a column Y's delete statement but after a column Z's add statement.
	for _, t := range old.TableDependencies {
		if !t.Kind.IsTable() {
			// A view reads other views and materialized views too, but only a table's columns are
			// modelled per column, and a non-table dependency's recreation is not cascaded yet.
			continue
		}
		if _, ok := deletedTablesByName[t.GetName()]; ok {
			// Recreate if a dependent table was deleted (or recreated).
			return viewDiff{}, true, nil
		}
		// It's possible a dependent column was deleted (or recreated).
		td, ok := tableDiffsByName[t.GetName()]
		if !ok {
			return viewDiff{}, false, fmt.Errorf("processing view table dependencies: expected a table diff to exist for %q. have=\n%s", t.GetName(), slices.Sorted(maps.Keys(tableDiffsByName)))
		}
		deletedColumnsByName := buildSchemaObjByNameMap(td.columnsDiff.deletes)
		for _, c := range t.Columns {
			if _, ok := deletedColumnsByName[c]; ok {
				// Recreate if a dependent column was deleted (or recreated).
				return viewDiff{}, true, nil
			}
		}
	}

	privilegesDiff, err := diffLists(
		old.Privileges,
		new.Privileges,
		func(old, new schema.TablePrivilege, _, _ int) (privilegeDiff, bool, error) {
			// Recreate the privilege if IsGrantable changes
			recreate := old.IsGrantable != new.IsGrantable
			return privilegeDiff{oldAndNew[schema.TablePrivilege]{old: old, new: new}}, recreate, nil
		},
	)
	if err != nil {
		return viewDiff{}, false, fmt.Errorf("diffing privileges: %w", err)
	}

	// Recreate if the view SQL generator cannot alter the view.
	d := viewDiff{
		oldAndNew:               oldAndNew[schema.View]{old: old, new: new},
		privilegesDiff:          privilegesDiff,
		stableTableDependencies: stableTableDependencies(new.TableDependencies, tableDiffsByName),
	}
	if _, err := newViewSQLVertexGenerator().Alter(d); err != nil {
		if errors.Is(err, ErrNotImplemented) {
			// The SQL generator cannot alter the view, so add and delete it.
			return viewDiff{}, true, nil
		}
		return viewDiff{}, false, fmt.Errorf("generating view alter statements: %w", err)
	}
	return d, false, nil
}

// stableTableDependencies returns the tables among dependencies that exist before the plan and
// whose columns the view reads are neither added nor changed by it.
func stableTableDependencies(dependencies []schema.TableDependency, tableDiffsByName map[string]tableDiff) map[string]bool {
	stable := make(map[string]bool)
	for _, t := range dependencies {
		if !t.Kind.IsTable() {
			continue
		}
		td, ok := tableDiffsByName[t.GetName()]
		if !ok {
			// A table that is added (or re-created) by the plan.
			continue
		}
		addedColumnsByName := buildSchemaObjByNameMap(td.columnsDiff.adds)
		alteredColumnsByName := buildDiffByNameMap[schema.Column, columnDiff](td.columnsDiff.alters)
		changed := false
		for _, c := range t.Columns {
			if _, ok := addedColumnsByName[c]; ok {
				changed = true
				break
			}
			if cd, ok := alteredColumnsByName[c]; ok && !cmp.Equal(cd.old, cd.new) {
				changed = true
				break
			}
		}
		if !changed {
			stable[t.GetName()] = true
		}
	}
	return stable
}

type viewSQLGenerator struct {
}

func newViewSQLVertexGenerator() sqlVertexGenerator[schema.View, viewDiff] {
	return &viewSQLGenerator{}
}

func (vsg *viewSQLGenerator) Add(v schema.View) (partialSQLGraph, error) {
	stmts := []Statement{{
		DDL:         buildViewDefinitionDDL(v, false),
		Timeout:     statementTimeoutDefault,
		LockTimeout: lockTimeoutDefault,
	}}

	privilegeGenerator := newPrivilegeSQLVertexGenerator(v.SchemaQualifiedName)
	for _, privilege := range v.Privileges {
		addPrivilegePartialGraph, err := privilegeGenerator.Add(privilege)
		if err != nil {
			return partialSQLGraph{}, fmt.Errorf("generating add privilege statements for privilege %s: %w", privilege.GetName(), err)
		}
		// Remove hazards from statements since the view is brand new
		stmts = append(stmts, stripMigrationHazards(addPrivilegePartialGraph.statements()...)...)
	}

	addVertexId := buildTableVertexId(v.SchemaQualifiedName, diffTypeAddAlter)

	var deps []dependency

	// Run after re-create (if recreated).
	deps = append(deps, mustRun(addVertexId).after(buildViewVertexId(v.SchemaQualifiedName, diffTypeDelete)))

	// Run after any dependent relations are added/altered.
	for _, t := range v.TableDependencies {
		deps = append(deps, mustRun(addVertexId).after(buildDependencyVertexId(t, diffTypeDelete)))
		deps = append(deps, mustRun(addVertexId).after(buildDependencyVertexId(t, diffTypeAddAlter)))
	}
	// Run after the functions the definition calls exist.
	for _, f := range v.DependsOnFunctions {
		deps = append(deps, mustRun(addVertexId).after(buildFunctionVertexId(f, diffTypeAddAlter)))
	}

	stmts = append(stmts, ownerDDLForAdd(ownershipTarget("VIEW", v.SchemaQualifiedName), v.Owner)...)
	stmts = append(stmts, commentDDLForAdd(commentTargetView(v.SchemaQualifiedName), v.Description)...)
	stmts = append(stmts, viewColumnCommentDDLForAdd(v.SchemaQualifiedName, v.Columns)...)

	return partialSQLGraph{
		vertices: []sqlVertex{{
			id:         addVertexId,
			priority:   sqlPrioritySooner,
			statements: stmts,
		}},
		dependencies: deps,
	}, nil
}

func (vsg *viewSQLGenerator) Delete(v schema.View) (partialSQLGraph, error) {
	deleteVertexId := buildViewVertexId(v.SchemaQualifiedName, diffTypeDelete)

	// Run before any dependent tables are deleted or added/altered.
	var deps []dependency
	for _, t := range v.TableDependencies {
		deps = append(deps, mustRun(deleteVertexId).before(buildDependencyVertexId(t, diffTypeDelete)))
		deps = append(deps, mustRun(deleteVertexId).before(buildDependencyVertexId(t, diffTypeAddAlter)))
	}
	// Run before the functions the definition calls are dropped.
	for _, f := range v.DependsOnFunctions {
		deps = append(deps, mustRun(deleteVertexId).before(buildFunctionVertexId(f, diffTypeDelete)))
	}

	return partialSQLGraph{
		vertices: []sqlVertex{{
			id:       deleteVertexId,
			priority: sqlPriorityLater,
			statements: []Statement{{
				DDL:         fmt.Sprintf("DROP VIEW %s", v.GetFQEscapedName()),
				Timeout:     statementTimeoutDefault,
				LockTimeout: lockTimeoutDefault,
			}},
		}},
		dependencies: deps,
	}, nil
}

func (vsg *viewSQLGenerator) Alter(vd viewDiff) (partialSQLGraph, error) {
	// Mask Privileges (handled by the privilege generator below), Description and Owner (handled
	// by explicit COMMENT / OWNER TO statements), and the output columns and the relations and
	// functions the view reads (all follow from the definition), so the structural-equality check below only triggers
	// ErrNotImplemented when something we cannot alter with a statement here changed. A view that
	// starts reading another relation under the same output columns is still replaced in place.
	//
	// The definition is compared through its canonical form, not through its
	// text: a view holds whatever pg_get_viewdef returned for it, and that text is
	// not necessarily its own output, so two databases can hold the same view
	// under two texts. Text equality would read that as a change and re-create the
	// view, whose new text would differ again from the one it was created from.
	oldMasked := vd.old
	oldMasked.Privileges = nil
	oldMasked.Description = vd.new.Description
	oldMasked.Owner = vd.new.Owner
	oldMasked.Columns = nil
	oldMasked.TableDependencies = nil
	oldMasked.DependsOnFunctions = nil
	maskViewDefinition(&oldMasked.ViewDefinition, &oldMasked.ViewDefinitionCanonical)
	newMasked := vd.new
	newMasked.Privileges = nil
	newMasked.Columns = nil
	newMasked.TableDependencies = nil
	newMasked.DependsOnFunctions = nil
	maskViewDefinition(&newMasked.ViewDefinition, &newMasked.ViewDefinitionCanonical)

	// Everything but the definition has to be resolvable by the explicit statements below; an
	// option change, for instance, has no statement here and forces a recreation.
	oldWithoutDefinition := oldMasked
	oldWithoutDefinition.ViewDefinitionCanonical = ""
	newWithoutDefinition := newMasked
	newWithoutDefinition.ViewDefinitionCanonical = ""
	if !cmp.Equal(oldWithoutDefinition, newWithoutDefinition) {
		return partialSQLGraph{}, ErrNotImplemented
	}

	definitionChanged := oldMasked.ViewDefinitionCanonical != newMasked.ViewDefinitionCanonical
	if definitionChanged && !viewColumnsCompatible(vd.old.Columns, vd.new.Columns) {
		// Replacing the view in place is allowed only while every existing output column keeps its
		// name and type, in order. A removed, reordered, or retyped column needs a drop and a
		// re-create (and a re-create of the views that read this one).
		return partialSQLGraph{}, ErrNotImplemented
	}

	privilegeGenerator := newPrivilegeSQLVertexGenerator(vd.new.SchemaQualifiedName)
	privilegesPartialGraph, err := generatePartialGraph(privilegeGenerator, vd.privilegesDiff)
	if err != nil {
		return partialSQLGraph{}, fmt.Errorf("resolving privilege sql: %w", err)
	}

	var stmts []Statement
	if definitionChanged {
		// A definition change that keeps the output columns is replaced in place. A drop-and-create
		// would fail outright whenever an unchanged view reads this one.
		stmts = append(stmts, Statement{
			DDL:         buildViewDefinitionDDL(vd.new, true),
			Timeout:     statementTimeoutDefault,
			LockTimeout: lockTimeoutDefault,
		})
	}
	stmts = append(stmts, ownerDDLForAlter(ownershipTarget("VIEW", vd.new.SchemaQualifiedName), vd.old.Owner, vd.new.Owner)...)
	stmts = append(stmts, commentDDLForAlter(commentTargetView(vd.new.SchemaQualifiedName), vd.old.Description, vd.new.Description)...)
	stmts = append(stmts, viewColumnCommentDDLForAlter(vd.new.SchemaQualifiedName, vd.old.Columns, vd.new.Columns)...)
	if len(stmts) > 0 {
		alterVertexId := buildTableVertexId(vd.new.SchemaQualifiedName, diffTypeAddAlter)
		privilegesPartialGraph.vertices = append(privilegesPartialGraph.vertices, sqlVertex{
			id:         alterVertexId,
			priority:   sqlPrioritySooner,
			statements: stmts,
		})
		if definitionChanged {
			privilegesPartialGraph.dependencies = append(privilegesPartialGraph.dependencies,
				replacedViewDependencies(alterVertexId, vd.old.TableDependencies, vd.new.TableDependencies, vd.stableTableDependencies)...)
			privilegesPartialGraph.dependencies = append(privilegesPartialGraph.dependencies,
				replacedViewFunctionDependencies(alterVertexId, vd.old.DependsOnFunctions, vd.new.DependsOnFunctions)...)
		}
	}

	return privilegesPartialGraph, nil
}

// replacedViewDependencies orders a view's in-place replacement among the relations its old and
// new definitions read. PostgreSQL resolves the new definition when it replaces the view, so every
// relation the new definition reads must already be in its new shape; a table whose read columns
// the plan leaves alone already is, and is left unordered. A view or materialized view
// only the old definition reads is still a dependency of the view until the replacement, so it can
// be dropped only afterwards. A table only the old definition reads needs no such edge: a view
// that read a dropped table is re-created rather than replaced (see buildViewDiff), and the table
// generator's delete vertex exists for every table, so the edge would tie the replacement to
// tables the plan does not drop.
func replacedViewDependencies(alterVertexId sqlVertexId, oldDependencies, newDependencies []schema.TableDependency, stableTables map[string]bool) []dependency {
	var deps []dependency
	newDependencyNames := make(map[string]bool)
	for _, t := range newDependencies {
		newDependencyNames[t.GetName()] = true
		if stableTables[t.GetName()] {
			// Every table has a delete and an add-or-alter vertex, so an edge to an unchanged one
			// would tie the replacement to whatever the plan orders around that table — for
			// instance, the drop of a view the replacement stops reading, which runs before the
			// table's vertices — and close a cycle.
			continue
		}
		deps = append(deps, mustRun(alterVertexId).after(buildDependencyVertexId(t, diffTypeDelete)))
		deps = append(deps, mustRun(alterVertexId).after(buildDependencyVertexId(t, diffTypeAddAlter)))
	}
	for _, t := range oldDependencies {
		if newDependencyNames[t.GetName()] || t.Kind.IsTable() {
			continue
		}
		deps = append(deps, mustRun(alterVertexId).before(buildDependencyVertexId(t, diffTypeDelete)))
	}
	return deps
}

// buildViewDefinitionDDL renders the statement that creates a view from its definition. A plain
// create is used for a new view; `CREATE OR REPLACE` replaces an existing one whose output columns
// are compatible (see viewColumnsCompatible).
func buildViewDefinitionDDL(v schema.View, orReplace bool) string {
	sb := strings.Builder{}
	if orReplace {
		sb.WriteString("CREATE OR REPLACE VIEW ")
	} else {
		sb.WriteString("CREATE VIEW ")
	}
	sb.WriteString(v.GetFQEscapedName())
	if len(v.Options) > 0 {
		var kvs []string
		for k, val := range v.Options {
			kvs = append(kvs, fmt.Sprintf("%s=%s", k, val))
		}
		// Sort kvs so the generated DDL is deterministic.
		slices.Sort(kvs)
		sb.WriteString(fmt.Sprintf(" WITH (%s)", strings.Join(kvs, ", ")))
	}
	sb.WriteString(" AS\n")
	sb.WriteString(v.ViewDefinition)
	return sb.String()
}

// viewColumnsCompatible reports whether `CREATE OR REPLACE VIEW` can replace a view with the old
// output columns by one with the new ones. PostgreSQL allows it while every existing column keeps
// its name and type, in order, with new columns appended; anything else — a removed, reordered, or
// retyped column — is rejected. A column's comment is not part of this: it is set by its own
// statement, and a replacement keeps the comments on the columns it keeps.
func viewColumnsCompatible(oldColumns, newColumns []schema.ViewColumn) bool {
	if len(newColumns) < len(oldColumns) {
		return false
	}
	for i, oldColumn := range oldColumns {
		if newColumns[i].Name != oldColumn.Name || newColumns[i].Type != oldColumn.Type {
			return false
		}
	}
	return true
}

// viewColumnCommentDDLForAdd sets the comments on a new view's or materialized view's columns.
func viewColumnCommentDDLForAdd(name schema.SchemaQualifiedName, columns []schema.ViewColumn) []Statement {
	var stmts []Statement
	for _, c := range columns {
		stmts = append(stmts, commentDDLForAdd(commentTargetColumn(name, c.Name), c.Description)...)
	}
	return stmts
}

// viewColumnCommentDDLForAlter brings the comments on a kept view's or materialized view's columns
// from old to new, matching columns by name. A column the new definition adds starts without a
// comment.
func viewColumnCommentDDLForAlter(name schema.SchemaQualifiedName, oldColumns, newColumns []schema.ViewColumn) []Statement {
	oldDescriptions := make(map[string]string)
	for _, c := range oldColumns {
		oldDescriptions[c.Name] = c.Description
	}
	var stmts []Statement
	for _, c := range newColumns {
		stmts = append(stmts, commentDDLForAlter(commentTargetColumn(name, c.Name), oldDescriptions[c.Name], c.Description)...)
	}
	return stmts
}

// maskViewDefinition replaces a definition and its canonical form with the one
// value a diff compares: the canonical form, or the definition itself when the
// schema carries none. A fetched schema always carries one; a schema built in
// memory, which is possible only inside this module, does not, and is then
// compared by its text as before.
func maskViewDefinition(definition, canonical *string) {
	if *canonical == "" {
		*canonical = *definition
	}
	*definition = ""
}

func buildViewVertexId(n schema.SchemaQualifiedName, d diffType) sqlVertexId {
	return buildSchemaObjVertexId("view", n.GetFQEscapedName(), d)
}

// cascadeRecreatedRelationViews re-creates every view and materialized view that reads a view or
// materialized view already being re-created, or that calls a function being dropped and keeps
// calling it. A re-created object is dropped and created again, and PostgreSQL refuses the drop
// while a dependent still reads it, so the dependent has to be dropped first and created again
// afterwards, with everything it carries (its Add emits its grants, owner, comment and options).
// The view and materialized view SQL generators already order a dependent's drop before, and its
// create after, the object it reads; this only has to mark the dependents for re-creation,
// transitively (a view that reads a view that reads the re-created one).
func cascadeRecreatedRelationViews(
	viewDiffs listDiff[schema.View, viewDiff],
	materializedViewDiffs listDiff[schema.MaterializedView, materializedViewDiff],
	functionDiffs listDiff[schema.Function, functionDiff],
) (listDiff[schema.View, viewDiff], listDiff[schema.MaterializedView, materializedViewDiff]) {
	droppedFunctions := make(map[string]bool)
	for _, f := range functionDiffs.deletes {
		droppedFunctions[f.GetName()] = true
	}

	recreated := make(map[string]bool)
	for _, v := range viewDiffs.deletes {
		recreated[v.GetName()] = true
	}
	for _, mv := range materializedViewDiffs.deletes {
		recreated[mv.GetName()] = true
	}

	for {
		progressed := false

		var remainingViewAlters []viewDiff
		for _, alter := range viewDiffs.alters {
			if readsRecreatedRelation(sharedDependencies(alter.old.TableDependencies, alter.new.TableDependencies), recreated) ||
				viewKeepsCallingDroppedFunction(alter.old, alter.new, droppedFunctions) {
				viewDiffs.deletes = append(viewDiffs.deletes, alter.old)
				viewDiffs.adds = append(viewDiffs.adds, alter.new)
				recreated[alter.new.GetName()] = true
				progressed = true
				continue
			}
			remainingViewAlters = append(remainingViewAlters, alter)
		}
		viewDiffs.alters = remainingViewAlters

		var remainingMaterializedViewAlters []materializedViewDiff
		for _, alter := range materializedViewDiffs.alters {
			// A materialized view is never replaced in place: an altered one keeps its definition, so
			// it keeps calling every function it called.
			if readsRecreatedRelation(sharedDependencies(alter.old.TableDependencies, alter.new.TableDependencies), recreated) ||
				callsDroppedFunction(alter.old.DependsOnFunctions, droppedFunctions) {
				materializedViewDiffs.deletes = append(materializedViewDiffs.deletes, alter.old)
				materializedViewDiffs.adds = append(materializedViewDiffs.adds, alter.new)
				recreated[alter.new.GetName()] = true
				progressed = true
				continue
			}
			remainingMaterializedViewAlters = append(remainingMaterializedViewAlters, alter)
		}
		materializedViewDiffs.alters = remainingMaterializedViewAlters

		if !progressed {
			break
		}
	}

	// diffLists returns deletes sorted by name, and the SQL generator emits them in that order; keep
	// the order after appending the cascaded ones.
	sort.Slice(viewDiffs.deletes, func(i, j int) bool {
		return viewDiffs.deletes[i].GetName() < viewDiffs.deletes[j].GetName()
	})
	sort.Slice(materializedViewDiffs.deletes, func(i, j int) bool {
		return materializedViewDiffs.deletes[i].GetName() < materializedViewDiffs.deletes[j].GetName()
	})

	return viewDiffs, materializedViewDiffs
}

// replacedViewFunctionDependencies orders a view's in-place replacement among the functions its old
// and new definitions call: after the functions the new definition calls are in their new shape,
// and before the functions only the old definition calls are dropped.
func replacedViewFunctionDependencies(alterVertexId sqlVertexId, oldFunctions, newFunctions []schema.SchemaQualifiedName) []dependency {
	var deps []dependency
	newFunctionNames := make(map[string]bool)
	for _, f := range newFunctions {
		newFunctionNames[f.GetName()] = true
		deps = append(deps, mustRun(alterVertexId).after(buildFunctionVertexId(f, diffTypeAddAlter)))
	}
	for _, f := range oldFunctions {
		if newFunctionNames[f.GetName()] {
			continue
		}
		deps = append(deps, mustRun(alterVertexId).before(buildFunctionVertexId(f, diffTypeDelete)))
	}
	return deps
}

// sharedDependencies returns the relations both the old and the new definition read. Only those
// pin a reader to a re-created relation: a relation that only the old definition reads is left
// before it is dropped, and one that only the new definition reads is read after it is created,
// both by the in-place replacement's own ordering (see replacedViewDependencies).
func sharedDependencies(oldDependencies, newDependencies []schema.TableDependency) []schema.TableDependency {
	newDependencyNames := make(map[string]bool)
	for _, t := range newDependencies {
		newDependencyNames[t.GetName()] = true
	}
	var shared []schema.TableDependency
	for _, t := range oldDependencies {
		if newDependencyNames[t.GetName()] {
			shared = append(shared, t)
		}
	}
	return shared
}

// viewKeepsCallingDroppedFunction reports whether a view that is not re-created would still depend
// on a function the plan drops when that drop runs. The view is replaced in place only when its
// definition changed. Then a dropped function pins it when the new definition calls a function of
// the same name: either the same signature, dropped and created again, or a new signature whose
// create the plan orders after the old one's drop (see orderFunctionDropsBeforeFunctionCreates), so
// the replacement could run neither before the drop nor after the create. When its definition is
// unchanged, every dropped function it calls pins it.
func viewKeepsCallingDroppedFunction(old, new schema.View, droppedFunctions map[string]bool) bool {
	if !viewDefinitionChanged(old, new) {
		return callsDroppedFunction(old.DependsOnFunctions, droppedFunctions)
	}
	newFunctionBareNames := make(map[string]bool)
	for _, f := range new.DependsOnFunctions {
		newFunctionBareNames[buildFunctionBareNameId(f)] = true
	}
	for _, f := range old.DependsOnFunctions {
		if droppedFunctions[f.GetName()] && newFunctionBareNames[buildFunctionBareNameId(f)] {
			return true
		}
	}
	return false
}

// callsDroppedFunction reports whether any of functions is dropped by the plan.
func callsDroppedFunction(functions []schema.SchemaQualifiedName, droppedFunctions map[string]bool) bool {
	for _, f := range functions {
		if droppedFunctions[f.GetName()] {
			return true
		}
	}
	return false
}

// viewDefinitionChanged reports whether a view's definition differs between old and new, compared
// the way the view generator compares it (see maskViewDefinition).
func viewDefinitionChanged(old, new schema.View) bool {
	oldDefinition, oldCanonical := old.ViewDefinition, old.ViewDefinitionCanonical
	maskViewDefinition(&oldDefinition, &oldCanonical)
	newDefinition, newCanonical := new.ViewDefinition, new.ViewDefinitionCanonical
	maskViewDefinition(&newDefinition, &newCanonical)
	return oldCanonical != newCanonical
}

// readsRecreatedRelation reports whether any of the relations a view reads is being re-created.
func readsRecreatedRelation(dependencies []schema.TableDependency, recreated map[string]bool) bool {
	for _, dependency := range dependencies {
		if recreated[dependency.GetName()] {
			return true
		}
	}
	return false
}
