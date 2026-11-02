package diff

import (
	"fmt"
	"strings"

	"github.com/google/go-cmp/cmp"
	"github.com/stripe/pg-schema-diff/internal/schema"
)

// compositeTypeSQLVertexGenerator handles `CREATE TYPE foo AS (...)`, `DROP TYPE foo` and
// `ALTER TYPE foo ADD ATTRIBUTE` for user-defined composite types. It is a
// SQLVertexGenerator (rather than a plain SQLGenerator) so it can carry explicit ordering
// dependencies relative to the consumers of a composite type — tables, views, materialized views,
// functions, procedures and other composite types, which may reference the type in column types,
// definitions, parameter types or return types.
//
// A change to a type's attribute list takes one of two paths. Adding attributes at the end needs no
// dependent to be touched: `ALTER TYPE ... ADD ATTRIBUTE` keeps the type's OID, and PostgreSQL
// never blocks it, whereas it refuses to drop or retype an attribute a view or rule reads. Every
// other change — a dropped, retyped, renamed or inserted attribute, or a reorder — is resolved by
// dropping and creating the type again, with every tracked dependent dropped first and created
// afterwards: PostgreSQL refuses the drop while anything still references the type. Re-creating a
// type that a table column uses is refused up front (see identifyCompositeTypesToRecreate).
type compositeTypeSQLVertexGenerator struct {
	// newSchema and oldSchema are used to set up dependency edges between a composite type and the
	// objects that reference it. Only the objects whose recorded dependencies name the type get an
	// edge; a blanket edge to every table or function would close a cycle with the type's own drop
	// for a re-created type (typeAdd → consumerAdd → typeDelete → typeAdd) and is not needed by
	// any of them.
	newSchema schema.Schema
	oldSchema schema.Schema
	// recreatedCompositeTypes are the types being dropped and created again. A consumer that
	// depends on any of them is re-created too, even when it stops naming the type whose drop is
	// being ordered here: its create already runs after that type's create, so ordering it before
	// the drop would close a cycle through another re-created type.
	recreatedCompositeTypes map[string]bool
}

func newCompositeTypeSQLVertexGenerator(oldSchema, newSchema schema.Schema, recreatedCompositeTypes map[string]bool) sqlVertexGenerator[schema.CompositeType, compositeTypeDiff] {
	return &compositeTypeSQLVertexGenerator{
		newSchema:               newSchema,
		oldSchema:               oldSchema,
		recreatedCompositeTypes: recreatedCompositeTypes,
	}
}

func (c *compositeTypeSQLVertexGenerator) Add(ct schema.CompositeType) (partialSQLGraph, error) {
	addVertexId := buildCompositeTypeVertexId(ct.SchemaQualifiedName, diffTypeAddAlter)

	stmts := []Statement{{
		DDL:         buildCreateCompositeTypeDDL(ct),
		Timeout:     statementTimeoutDefault,
		LockTimeout: lockTimeoutDefault,
	}}
	stmts = append(stmts, commentDDLForAdd(commentTargetType(ct.SchemaQualifiedName), ct.Description)...)

	// The type must exist before any new consumer is created or altered to use it.
	deps := c.consumerDepsForAddAlter(ct)
	deps = append(deps, c.compositeTypeDepsForAddAlter(ct)...)
	// Run after re-create (if recreated). Mirrors the view/mview/domain pattern.
	deps = append(deps, mustRun(addVertexId).after(buildCompositeTypeVertexId(ct.SchemaQualifiedName, diffTypeDelete)))

	return partialSQLGraph{
		vertices: []sqlVertex{{
			id:         addVertexId,
			priority:   sqlPrioritySooner,
			statements: stmts,
		}},
		dependencies: deps,
	}, nil
}

func (c *compositeTypeSQLVertexGenerator) Delete(ct schema.CompositeType) (partialSQLGraph, error) {
	deleteVertexId := buildCompositeTypeVertexId(ct.SchemaQualifiedName, diffTypeDelete)

	// The type must be dropped after every consumer (in the OLD schema) has been dropped or altered
	// to no longer reference it.
	deps := c.consumerDepsForDelete(ct)
	deps = append(deps, c.compositeTypeDepsForDelete(ct)...)

	return partialSQLGraph{
		vertices: []sqlVertex{{
			id:       deleteVertexId,
			priority: sqlPriorityDropsLast,
			statements: []Statement{{
				DDL:         fmt.Sprintf("DROP TYPE %s", ct.GetFQEscapedName()),
				Timeout:     statementTimeoutDefault,
				LockTimeout: lockTimeoutDefault,
			}},
		}},
		dependencies: deps,
	}, nil
}

func (c *compositeTypeSQLVertexGenerator) Alter(d compositeTypeDiff) (partialSQLGraph, error) {
	// Comment-only diffs are emitted as a COMMENT ON TYPE statement and do not require touching
	// the type itself.
	if cmp.Equal(d.old.Attributes, d.new.Attributes) {
		commentStmts := commentDDLForAlter(commentTargetType(d.new.SchemaQualifiedName), d.old.Description, d.new.Description)
		if len(commentStmts) == 0 {
			return partialSQLGraph{}, nil
		}
		return partialSQLGraph{
			vertices: []sqlVertex{{
				id:         buildCompositeTypeVertexId(d.new.SchemaQualifiedName, diffTypeAddAlter),
				priority:   sqlPrioritySooner,
				statements: commentStmts,
			}},
		}, nil
	}

	added, ok := appendedCompositeTypeAttributes(d.old.Attributes, d.new.Attributes)
	if !ok {
		// identifyCompositeTypesToRecreate marks such a type for a DROP + CREATE, so Alter is only
		// reached for a layout PostgreSQL can reproduce with ALTER TYPE.
		return partialSQLGraph{}, fmt.Errorf("altering composite type attributes: %w", ErrNotImplemented)
	}

	alterVertexId := buildCompositeTypeVertexId(d.new.SchemaQualifiedName, diffTypeAddAlter)
	stmts := buildAlterCompositeTypeAttributeStatements(d.new, added)
	stmts = append(stmts, commentDDLForAlter(commentTargetType(d.new.SchemaQualifiedName), d.old.Description, d.new.Description)...)

	// An attribute added to the type is resolved against the new layout by anything created
	// afterwards, so every new consumer has to run after the ALTER as well.
	deps := c.consumerDepsForAddAlter(d.new)
	deps = append(deps, c.compositeTypeDepsForAddAlter(d.new)...)

	return partialSQLGraph{
		vertices: []sqlVertex{{
			id:         alterVertexId,
			priority:   sqlPrioritySooner,
			statements: stmts,
		}},
		dependencies: deps,
	}, nil
}

func buildCompositeTypeVertexId(name schema.SchemaQualifiedName, d diffType) sqlVertexId {
	return buildSchemaObjVertexId("composite_type", name.GetFQEscapedName(), d)
}

func buildCreateCompositeTypeDDL(ct schema.CompositeType) string {
	if len(ct.Attributes) == 0 {
		// PostgreSQL does not allow `CREATE TYPE foo AS ()` with zero columns at
		// creation time, but it does allow ALTER TYPE ... DROP ATTRIBUTE down
		// to zero. Such a state is not reachable from a declarative schema
		// (which always describes what it wants from scratch), so emitting an
		// empty parens is fine — apply will fail with a clear PG error if it
		// ever happens.
		return fmt.Sprintf("CREATE TYPE %s AS ()", ct.GetFQEscapedName())
	}
	var attrDefs []string
	for _, a := range ct.Attributes {
		attrDefs = append(attrDefs, buildCompositeTypeAttributeDef(a))
	}
	return fmt.Sprintf("CREATE TYPE %s AS (\n%s\n)", ct.GetFQEscapedName(), strings.Join(attrDefs, ",\n"))
}

func buildCompositeTypeAttributeDef(a schema.CompositeTypeAttribute) string {
	def := fmt.Sprintf("\t%s %s", schema.EscapeIdentifier(a.Name), a.Type)
	if !a.Collation.IsEmpty() {
		def += fmt.Sprintf(" COLLATE %s", a.Collation.GetFQEscapedName())
	}
	return def
}

// appendedCompositeTypeAttributes returns the attributes that turn oldAttributes into
// newAttributes, and whether ALTER TYPE can do it. PostgreSQL appends every added attribute after
// the existing ones and cannot reorder, insert in the middle, drop or retype an attribute that a
// view or a rule reads (it refuses with "cannot drop/alter ... because other objects depend on
// it"). Attribute-level reads are not modelled here — only the type-level dependency is — so the
// in-place path is limited to the change that can never be blocked: the old layout is a prefix of
// the new one and the additions are the exact new suffix. Anything else is resolved by
// dropping and re-creating the type with its dependents.
func appendedCompositeTypeAttributes(oldAttributes, newAttributes []schema.CompositeTypeAttribute) ([]schema.CompositeTypeAttribute, bool) {
	if len(newAttributes) < len(oldAttributes) {
		return nil, false
	}
	for i, oldAttr := range oldAttributes {
		if !cmp.Equal(oldAttr, newAttributes[i]) {
			return nil, false
		}
	}
	return newAttributes[len(oldAttributes):], true
}

// buildAlterCompositeTypeAttributeStatements renders the ALTER TYPE statements that append the
// given attributes.
func buildAlterCompositeTypeAttributeStatements(ct schema.CompositeType, added []schema.CompositeTypeAttribute) []Statement {
	alterPrefix := fmt.Sprintf("ALTER TYPE %s", ct.GetFQEscapedName())
	var stmts []Statement
	for _, attr := range added {
		ddl := fmt.Sprintf("%s ADD ATTRIBUTE %s %s", alterPrefix, schema.EscapeIdentifier(attr.Name), attr.Type)
		if !attr.Collation.IsEmpty() {
			ddl += fmt.Sprintf(" COLLATE %s", attr.Collation.GetFQEscapedName())
		}
		stmts = append(stmts, Statement{
			DDL:         ddl,
			Timeout:     statementTimeoutDefault,
			LockTimeout: lockTimeoutDefault,
		})
	}
	return stmts
}

// dependsOnAnyRecreatedType reports whether any of the given composite-type references is in the
// set of types being recreated (their attribute list is changing). Used to force-recreate functions
// and procedures so PostgreSQL resolves their argument/return types against the new layout.
func dependsOnAnyRecreatedType(deps []schema.SchemaQualifiedName, recreated map[string]bool) bool {
	for _, d := range deps {
		if recreated[d.GetName()] {
			return true
		}
	}
	return false
}

func dependsOnCompositeType(deps []schema.SchemaQualifiedName, ctName string) bool {
	for _, d := range deps {
		if d.GetName() == ctName {
			return true
		}
	}
	return false
}

// consumerDepsForAddAlter returns dependency edges that force the composite type's CREATE or ALTER
// to run before any new consumer that references the type is created or altered.
func (c *compositeTypeSQLVertexGenerator) consumerDepsForAddAlter(ct schema.CompositeType) []dependency {
	addVertexId := buildCompositeTypeVertexId(ct.SchemaQualifiedName, diffTypeAddAlter)
	ctName := ct.GetName()

	var deps []dependency
	for _, t := range c.newSchema.Tables {
		if !dependsOnCompositeType(t.DependsOnCompositeTypes, ctName) {
			continue
		}
		deps = append(deps, mustRun(addVertexId).before(buildTableVertexId(t.SchemaQualifiedName, diffTypeAddAlter)))
		// A column added to an existing table gets its own vertex, so the table vertex alone is
		// not enough to order the type before the column that uses it.
		for _, column := range t.Columns {
			deps = append(deps, mustRun(addVertexId).before(buildColumnVertexId(column.Name, diffTypeAddAlter)))
		}
	}
	for _, f := range c.newSchema.Functions {
		if dependsOnCompositeType(f.DependsOnCompositeTypes, ctName) {
			deps = append(deps, mustRun(addVertexId).before(buildFunctionVertexId(f.SchemaQualifiedName, diffTypeAddAlter)))
		}
	}
	for _, p := range c.newSchema.Procedures {
		if dependsOnCompositeType(p.DependsOnCompositeTypes, ctName) {
			deps = append(deps, mustRun(addVertexId).before(buildProcedureVertexId(p.SchemaQualifiedName, diffTypeAddAlter)))
		}
	}
	for _, v := range c.newSchema.Views {
		if dependsOnCompositeType(v.DependsOnCompositeTypes, ctName) {
			deps = append(deps, mustRun(addVertexId).before(buildTableVertexId(v.SchemaQualifiedName, diffTypeAddAlter)))
		}
	}
	for _, mv := range c.newSchema.MaterializedViews {
		if dependsOnCompositeType(mv.DependsOnCompositeTypes, ctName) {
			deps = append(deps, mustRun(addVertexId).before(buildMaterializedViewVertexId(mv.SchemaQualifiedName, diffTypeAddAlter)))
		}
	}
	for _, other := range c.newSchema.CompositeTypes {
		if dependsOnCompositeType(other.DependsOnCompositeTypes, ctName) {
			deps = append(deps, mustRun(addVertexId).before(buildCompositeTypeVertexId(other.SchemaQualifiedName, diffTypeAddAlter)))
		}
	}
	return deps
}

func (c *compositeTypeSQLVertexGenerator) compositeTypeDepsForAddAlter(ct schema.CompositeType) []dependency {
	addVertexId := buildCompositeTypeVertexId(ct.SchemaQualifiedName, diffTypeAddAlter)

	var deps []dependency
	for _, dep := range ct.DependsOnCompositeTypes {
		deps = append(deps, mustRun(addVertexId).after(buildCompositeTypeVertexId(dep, diffTypeAddAlter)))
	}
	return deps
}

// consumerDepsForDelete returns dependency edges that force the composite type's DROP to run after
// every consumer in the old schema is dropped or altered to no longer reference the type.
//
// When a consumer still references this type in the new schema — i.e. the type is being recreated
// because its attributes changed and the consumer is force-recreated alongside it — we must NOT add
// the `typeDelete > consumerAddAlter` edge: doing so would force the consumer to be created BEFORE
// the type is dropped, which contradicts the required order
//
//	consumerDelete < typeDelete < typeAdd < consumerAddAlter
//
// and closes a cycle. Tables are special only in that a table column typed with a re-created type is
// refused up front (see identifyCompositeTypesToRecreate), so a table that depends on a re-created
// type never reaches this code; a table that depends on a dropped type always loses the dependency.
func (c *compositeTypeSQLVertexGenerator) consumerDepsForDelete(ct schema.CompositeType) []dependency {
	deleteVertexId := buildCompositeTypeVertexId(ct.SchemaQualifiedName, diffTypeDelete)
	ctName := ct.GetName()

	newTablesByName := buildSchemaObjByNameMap(c.newSchema.Tables)
	newFunctionsByName := buildSchemaObjByNameMap(c.newSchema.Functions)
	newProceduresByName := buildSchemaObjByNameMap(c.newSchema.Procedures)
	newViewsByName := buildSchemaObjByNameMap(c.newSchema.Views)
	newMaterializedViewsByName := buildSchemaObjByNameMap(c.newSchema.MaterializedViews)
	newCompositeTypesByName := buildSchemaObjByNameMap(c.newSchema.CompositeTypes)

	// addAlterEdgeWouldCycle reports whether the consumer's add/alter vertex must not be ordered
	// after this type's drop. If the consumer depends on some re-created type — this one, or
	// another one it reaches through an outer type — it is re-created as well, and its create
	// already runs after that type's create; ordering the create before this type's drop and the
	// drop before the create is a cycle.
	addAlterEdgeWouldCycle := func(newDeps []schema.SchemaQualifiedName) bool {
		return dependsOnCompositeType(newDeps, ctName) ||
			dependsOnAnyRecreatedType(newDeps, c.recreatedCompositeTypes)
	}

	var deps []dependency
	for _, t := range c.oldSchema.Tables {
		if !dependsOnCompositeType(t.DependsOnCompositeTypes, ctName) {
			continue
		}
		deps = append(deps, mustRun(deleteVertexId).after(buildTableVertexId(t.SchemaQualifiedName, diffTypeDelete)))
		for _, column := range t.Columns {
			deps = append(deps, mustRun(deleteVertexId).after(buildColumnVertexId(column.Name, diffTypeDelete)))
			deps = append(deps, mustRun(deleteVertexId).after(buildColumnVertexId(column.Name, diffTypeAddAlter)))
		}
		// A table column typed with a re-created type is refused up-front, so a table that still
		// depends on the type here keeps it only when the type is not being dropped at all.
		if !addAlterEdgeWouldCycle(newTablesByName[t.GetName()].DependsOnCompositeTypes) {
			deps = append(deps, mustRun(deleteVertexId).after(buildTableVertexId(t.SchemaQualifiedName, diffTypeAddAlter)))
		}
	}
	for _, f := range c.oldSchema.Functions {
		if !dependsOnCompositeType(f.DependsOnCompositeTypes, ctName) {
			continue
		}
		deps = append(deps, mustRun(deleteVertexId).after(buildFunctionVertexId(f.SchemaQualifiedName, diffTypeDelete)))
		if !addAlterEdgeWouldCycle(newFunctionsByName[f.GetName()].DependsOnCompositeTypes) {
			deps = append(deps, mustRun(deleteVertexId).after(buildFunctionVertexId(f.SchemaQualifiedName, diffTypeAddAlter)))
		}
	}
	for _, p := range c.oldSchema.Procedures {
		if !dependsOnCompositeType(p.DependsOnCompositeTypes, ctName) {
			continue
		}
		deps = append(deps, mustRun(deleteVertexId).after(buildProcedureVertexId(p.SchemaQualifiedName, diffTypeDelete)))
		if !addAlterEdgeWouldCycle(newProceduresByName[p.GetName()].DependsOnCompositeTypes) {
			deps = append(deps, mustRun(deleteVertexId).after(buildProcedureVertexId(p.SchemaQualifiedName, diffTypeAddAlter)))
		}
	}
	for _, v := range c.oldSchema.Views {
		if !dependsOnCompositeType(v.DependsOnCompositeTypes, ctName) {
			continue
		}
		deps = append(deps, mustRun(deleteVertexId).after(buildViewVertexId(v.SchemaQualifiedName, diffTypeDelete)))
		if !addAlterEdgeWouldCycle(newViewsByName[v.GetName()].DependsOnCompositeTypes) {
			// The view is replaced in place, which drops the reference to the type.
			deps = append(deps, mustRun(deleteVertexId).after(buildTableVertexId(v.SchemaQualifiedName, diffTypeAddAlter)))
		}
	}
	for _, mv := range c.oldSchema.MaterializedViews {
		if !dependsOnCompositeType(mv.DependsOnCompositeTypes, ctName) {
			continue
		}
		deps = append(deps, mustRun(deleteVertexId).after(buildMaterializedViewVertexId(mv.SchemaQualifiedName, diffTypeDelete)))
		if !addAlterEdgeWouldCycle(newMaterializedViewsByName[mv.GetName()].DependsOnCompositeTypes) {
			deps = append(deps, mustRun(deleteVertexId).after(buildMaterializedViewVertexId(mv.SchemaQualifiedName, diffTypeAddAlter)))
		}
	}
	for _, other := range c.oldSchema.CompositeTypes {
		if !dependsOnCompositeType(other.DependsOnCompositeTypes, ctName) {
			continue
		}
		deps = append(deps, mustRun(deleteVertexId).after(buildCompositeTypeVertexId(other.SchemaQualifiedName, diffTypeDelete)))
		// A composite type still typed with a re-created type is force re-created alongside it
		// (see identifyCompositeTypesToRecreate), so the edge to its add/alter must be skipped:
		// it would contradict compositeTypeDelete < typeDelete < typeAdd < compositeTypeAdd.
		if !addAlterEdgeWouldCycle(newCompositeTypesByName[other.GetName()].DependsOnCompositeTypes) {
			deps = append(deps, mustRun(deleteVertexId).after(buildCompositeTypeVertexId(other.SchemaQualifiedName, diffTypeAddAlter)))
		}
	}
	return deps
}

func (c *compositeTypeSQLVertexGenerator) compositeTypeDepsForDelete(ct schema.CompositeType) []dependency {
	deleteVertexId := buildCompositeTypeVertexId(ct.SchemaQualifiedName, diffTypeDelete)

	var deps []dependency
	for _, dep := range ct.DependsOnCompositeTypes {
		deps = append(deps, mustRun(deleteVertexId).before(buildCompositeTypeVertexId(dep, diffTypeDelete)))
	}
	return deps
}
