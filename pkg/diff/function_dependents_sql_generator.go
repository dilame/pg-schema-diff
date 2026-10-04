package diff

import (
	"fmt"
	"sort"

	"github.com/stripe/pg-schema-diff/internal/schema"
)

// A function the plan drops — because it is re-created (a changed result type, a re-created type
// in its signature, a SQL-standard body over an altered column) or because its signature changes —
// cannot be dropped while anything still depends on it: PostgreSQL refuses the DROP FUNCTION
// (SQLSTATE 2BP01). Views, materialized views and SQL-standard function bodies that call it are
// re-created around it as whole objects (see cascadeRecreatedFunctions and
// cascadeRecreatedRelationViews). The objects below live inside a table, so the table's own
// statements cannot carry them: those run either before the drop or after the create, never both.
//
// Each such table gets a pair of vertices. The release vertex detaches the table's objects from
// the function before it is dropped: it drops the policies that call it and the column defaults
// that call it. The restore vertex runs after the new function exists and the table has reached its
// new shape, and attaches what the target declares in their place: each policy with its command,
// roles, USING and WITH CHECK expressions and comment, and each column default. The table's own
// diff no longer carries those objects, so it neither alters a policy the release drops nor sets a
// default the restore sets.
//
// A check constraint or an index that calls such a function is not re-created. Re-adding a check
// constraint validates every row of the table, and rebuilding an index takes as long as the table
// is large, without the index serving any query in between; both are long operations a plan should
// not slip in behind a function change. The plan fails instead, naming the function and the
// object, so the operator can rewrite or split the change.

// functionDependentsRebind is one table's objects that call a function the plan drops.
type functionDependentsRebind struct {
	oldTable schema.Table
	newTable schema.Table

	// policies are the table's policies, in the old schema, that call a dropped function. The ones
	// the new schema still declares under the same name are created again by the restore vertex.
	policies []schema.Policy
	// columns are the table's columns, in the old schema, whose default calls a dropped function.
	// The ones the new schema still declares with a default get it set again by the restore vertex.
	columns []schema.Column
}

// droppedFunctionNames returns the names (with their identity arguments) of the functions the plan
// drops.
func droppedFunctionNames(functionDiffs listDiff[schema.Function, functionDiff]) map[string]bool {
	dropped := make(map[string]bool)
	for _, f := range functionDiffs.deletes {
		dropped[f.GetName()] = true
	}
	return dropped
}

func callsAnyFunction(functions []schema.SchemaQualifiedName, names map[string]bool) bool {
	for _, f := range functions {
		if names[f.GetName()] {
			return true
		}
	}
	return false
}

// keepsCallingDroppedFunction reports whether an object that calls oldFunctions before the plan and
// newFunctions after it calls a function the plan drops on both sides: the same signature, dropped
// and created again, or a new signature under the same name, whose create the plan orders after the
// old one's drop (see orderFunctionDropsBeforeFunctionCreates). Such an object can be altered
// neither before the drop nor after the create, so it has to be re-created around them. An object
// that stops calling the dropped function is altered before the drop instead.
func keepsCallingDroppedFunction(oldFunctions, newFunctions []schema.SchemaQualifiedName, dropped map[string]bool) bool {
	newBareNames := make(map[string]bool)
	for _, f := range newFunctions {
		newBareNames[buildFunctionBareNameId(f)] = true
	}
	for _, f := range oldFunctions {
		if dropped[f.GetName()] && newBareNames[buildFunctionBareNameId(f)] {
			return true
		}
	}
	return false
}

// cascadeRecreatedFunctions re-creates every function that calls a function the plan drops, and
// keeps going for the functions that call those, transitively. Only a SQL-standard body (BEGIN
// ATOMIC) records the functions it calls, and PostgreSQL refuses to drop a function such a body
// calls. A re-created function is dropped before the functions it calls are dropped and created
// after the functions it calls are created (see functionSQLVertexGenerator), and its Add emits its
// owner, comment and privileges.
func cascadeRecreatedFunctions(functionDiffs listDiff[schema.Function, functionDiff]) listDiff[schema.Function, functionDiff] {
	dropped := droppedFunctionNames(functionDiffs)
	for {
		progressed := false
		var remaining []functionDiff
		for _, alter := range functionDiffs.alters {
			if callsAnyFunction(alter.old.DependsOnFunctions, dropped) {
				functionDiffs.deletes = append(functionDiffs.deletes, alter.old)
				functionDiffs.adds = append(functionDiffs.adds, alter.new)
				dropped[alter.old.GetName()] = true
				progressed = true
				continue
			}
			remaining = append(remaining, alter)
		}
		functionDiffs.alters = remaining
		if !progressed {
			break
		}
	}
	sort.Slice(functionDiffs.deletes, func(i, j int) bool {
		return functionDiffs.deletes[i].GetName() < functionDiffs.deletes[j].GetName()
	})
	sort.Slice(functionDiffs.adds, func(i, j int) bool {
		return functionDiffs.adds[i].GetName() < functionDiffs.adds[j].GetName()
	})
	return functionDiffs
}

// refuseUnrebindableFunctionDependents fails the plan when a check constraint or an index of a
// table the plan keeps calls a function the plan drops. See the comment at the top of this file.
func refuseUnrebindableFunctionDependents(
	tableDiffs listDiff[schema.Table, tableDiff],
	oldIndexes []schema.Index,
	dropped map[string]bool,
) error {
	keptTables := make(map[string]bool)
	for _, td := range tableDiffs.alters {
		keptTables[td.old.GetName()] = true
		for _, cc := range td.old.CheckConstraints {
			for _, f := range cc.DependsOnFunctions {
				if dropped[f.GetName()] {
					return fmt.Errorf("function %s is dropped by this plan, and check constraint %s on table %s calls it: "+
						"re-adding the constraint would validate every row of the table, so a plan does not re-create it: %w",
						f.GetFQEscapedName(), cc.Name, td.old.GetFQEscapedName(), ErrNotImplemented)
				}
			}
		}
	}
	for _, idx := range oldIndexes {
		if !keptTables[idx.OwningRelName.GetName()] {
			continue
		}
		for _, f := range idx.DependsOnFunctions {
			if dropped[f.GetName()] {
				return fmt.Errorf("function %s is dropped by this plan, and index %s on %s calls it: "+
					"rebuilding the index takes as long as the table is large and leaves queries without it in between, "+
					"so a plan does not re-create it: %w",
					f.GetFQEscapedName(), schema.EscapeIdentifier(idx.Name), idx.OwningRelName.GetFQEscapedName(), ErrNotImplemented)
			}
		}
	}
	return nil
}

// detachFunctionDependents takes the policies and column defaults that call a dropped function
// out of their tables' diffs and returns them as rebinds, one per table.
func detachFunctionDependents(
	tableDiffs listDiff[schema.Table, tableDiff],
	dropped map[string]bool,
) (listDiff[schema.Table, tableDiff], []functionDependentsRebind) {
	var rebinds []functionDependentsRebind
	for i, td := range tableDiffs.alters {
		rebind := functionDependentsRebind{oldTable: td.old, newTable: td.new}

		detachedPolicies := make(map[string]bool)
		for _, p := range td.old.Policies {
			if callsAnyFunction(p.DependsOnFunctions, dropped) {
				rebind.policies = append(rebind.policies, p)
				detachedPolicies[p.GetName()] = true
			}
		}
		if len(detachedPolicies) > 0 {
			td.policiesDiff = withoutPolicies(td.policiesDiff, detachedPolicies)
		}

		detachedColumns := make(map[string]bool)
		for _, c := range td.old.Columns {
			if callsAnyFunction(c.DefaultDependsOnFunctions, dropped) {
				rebind.columns = append(rebind.columns, c)
				detachedColumns[c.GetName()] = true
			}
		}
		if len(detachedColumns) > 0 {
			// The release drops the old default and the restore sets the new one, so the column's
			// own alter must do neither.
			for j, cd := range td.columnsDiff.alters {
				if detachedColumns[cd.old.GetName()] {
					cd.old.Default = cd.new.Default
					td.columnsDiff.alters[j] = cd
				}
			}
		}

		if len(rebind.policies) > 0 || len(rebind.columns) > 0 {
			tableDiffs.alters[i] = td
			rebinds = append(rebinds, rebind)
		}
	}
	return tableDiffs, rebinds
}

// withoutPolicies removes the named policies from every part of a policy diff.
func withoutPolicies(d listDiff[schema.Policy, policyDiff], names map[string]bool) listDiff[schema.Policy, policyDiff] {
	var out listDiff[schema.Policy, policyDiff]
	for _, p := range d.adds {
		if !names[p.GetName()] {
			out.adds = append(out.adds, p)
		}
	}
	for _, p := range d.deletes {
		if !names[p.GetName()] {
			out.deletes = append(out.deletes, p)
		}
	}
	for _, a := range d.alters {
		if !names[a.old.GetName()] {
			out.alters = append(out.alters, a)
		}
	}
	return out
}

func buildFunctionDependentsVertexId(table schema.SchemaQualifiedName, d diffType) sqlVertexId {
	return buildSchemaObjVertexId("function_dependents", table.GetFQEscapedName(), d)
}

// functionDependentsPartialGraph emits the release and restore vertices of every rebind.
func functionDependentsPartialGraph(rebinds []functionDependentsRebind) (partialSQLGraph, error) {
	var graph partialSQLGraph
	for _, rebind := range rebinds {
		g, err := rebind.partialGraph()
		if err != nil {
			return partialSQLGraph{}, fmt.Errorf("rebinding the objects of table %s: %w", rebind.oldTable.GetFQEscapedName(), err)
		}
		graph = concatPartialGraphs(graph, g)
	}
	return graph, nil
}

func (r functionDependentsRebind) partialGraph() (partialSQLGraph, error) {
	tableName := r.newTable.SchemaQualifiedName
	releaseId := buildFunctionDependentsVertexId(tableName, diffTypeDelete)
	restoreId := buildFunctionDependentsVertexId(tableName, diffTypeAddAlter)

	oldTable := r.oldTable
	policyGenerator, err := newPolicySQLVertexGenerator(&oldTable, r.newTable)
	if err != nil {
		return partialSQLGraph{}, fmt.Errorf("creating policy sql vertex generator: %w", err)
	}
	newPoliciesByName := buildSchemaObjByNameMap(r.newTable.Policies)
	newColumnsByName := buildSchemaObjByNameMap(r.newTable.Columns)

	var releaseStmts, restoreStmts []Statement
	deps := []dependency{
		mustRun(releaseId).before(restoreId),
		// The release runs before the table's own statements, which may drop a column whose
		// default it detaches; the restore runs after them, once every column it sets a default on
		// or a policy reads exists.
		mustRun(releaseId).before(buildTableVertexId(tableName, diffTypeAddAlter)),
		mustRun(restoreId).after(buildTableVertexId(tableName, diffTypeAddAlter)),
	}

	for _, p := range r.policies {
		g, err := policyGenerator.Delete(p)
		if err != nil {
			return partialSQLGraph{}, fmt.Errorf("generating drop of policy %s: %w", p.EscapedName, err)
		}
		releaseStmts = append(releaseStmts, g.statements()...)
		for _, f := range p.DependsOnFunctions {
			deps = append(deps, mustRun(releaseId).before(buildFunctionVertexId(f, diffTypeDelete)))
		}

		newPolicy, ok := newPoliciesByName[p.GetName()]
		if !ok {
			continue
		}
		g, err = policyGenerator.Add(newPolicy)
		if err != nil {
			return partialSQLGraph{}, fmt.Errorf("generating create of policy %s: %w", newPolicy.EscapedName, err)
		}
		restoreStmts = append(restoreStmts, g.statements()...)
		for _, f := range newPolicy.DependsOnFunctions {
			deps = append(deps, mustRun(restoreId).after(buildFunctionVertexId(f, diffTypeAddAlter)))
		}
		for _, rel := range newPolicy.DependsOnRelations {
			if rel.GetName() == tableName.GetName() {
				continue
			}
			deps = append(deps, mustRun(restoreId).after(buildRelationVertexId(rel, diffTypeAddAlter)))
		}
	}

	for _, c := range r.columns {
		releaseStmts = append(releaseStmts, Statement{
			DDL:         fmt.Sprintf("%s ALTER COLUMN %s DROP DEFAULT", alterTablePrefix(tableName), schema.EscapeIdentifier(c.Name)),
			Timeout:     statementTimeoutDefault,
			LockTimeout: lockTimeoutDefault,
		})
		for _, f := range c.DefaultDependsOnFunctions {
			deps = append(deps, mustRun(releaseId).before(buildFunctionVertexId(f, diffTypeDelete)))
		}

		newColumn, ok := newColumnsByName[c.GetName()]
		if !ok || newColumn.Default == "" {
			continue
		}
		restoreStmts = append(restoreStmts, Statement{
			DDL:         fmt.Sprintf("%s ALTER COLUMN %s SET DEFAULT %s", alterTablePrefix(tableName), schema.EscapeIdentifier(newColumn.Name), newColumn.Default),
			Timeout:     statementTimeoutDefault,
			LockTimeout: lockTimeoutDefault,
		})
		for _, f := range newColumn.DefaultDependsOnFunctions {
			deps = append(deps, mustRun(restoreId).after(buildFunctionVertexId(f, diffTypeAddAlter)))
		}
	}

	return partialSQLGraph{
		vertices: []sqlVertex{
			{id: releaseId, priority: sqlPriorityUnset, statements: releaseStmts},
			{id: restoreId, priority: sqlPrioritySooner, statements: restoreStmts},
		},
		dependencies: deps,
	}, nil
}
