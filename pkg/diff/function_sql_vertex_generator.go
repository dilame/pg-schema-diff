package diff

import (
	"fmt"
	"strings"

	"github.com/google/go-cmp/cmp"
	"github.com/stripe/pg-schema-diff/internal/schema"
)

type functionSQLVertexGenerator struct {
	// functionsInNewSchemaByName is a map of function name to functions in the new schema.
	// These functions are not necessarily new
	functionsInNewSchemaByName map[string]schema.Function
}

func newFunctionSqlVertexGenerator(functionsInNewSchemaByName map[string]schema.Function) sqlVertexGenerator[schema.Function, functionDiff] {
	return legacyToNewSqlVertexGenerator[schema.Function, functionDiff](&functionSQLVertexGenerator{
		functionsInNewSchemaByName: functionsInNewSchemaByName,
	})
}

func (f *functionSQLVertexGenerator) Add(function schema.Function) ([]Statement, error) {
	var hazards []MigrationHazard
	if !canFunctionDependenciesBeTracked(function) {
		hazards = append(hazards, MigrationHazard{
			Type: MigrationHazardTypeHasUntrackableDependencies,
			Message: "Dependencies, i.e. other functions used in the function body, of non-sql functions cannot be tracked. " +
				"As a result, we cannot guarantee that function dependencies are ordered properly relative to this " +
				"statement. For adds, this means you need to ensure that all functions this function depends on are " +
				"created/altered before this statement.",
		})
	}
	stmts := []Statement{{
		DDL:         function.FunctionDef,
		Timeout:     statementTimeoutDefault,
		LockTimeout: lockTimeoutDefault,
		Hazards:     hazards,
	}}
	stmts = append(stmts, ownerDDLForAdd(ownershipTarget("FUNCTION", function.SchemaQualifiedName), function.Owner)...)
	stmts = append(stmts, commentDDLForAdd(commentTargetFunction(function.SchemaQualifiedName), function.Description)...)
	privilegeStmts, err := exactRoutinePrivilegeStatements(
		newFunctionPrivilegeSQLVertexGenerator(function.SchemaQualifiedName),
		function.Privileges,
	)
	if err != nil {
		return nil, fmt.Errorf("generating function privilege statements: %w", err)
	}
	stmts = append(stmts, privilegeStmts...)
	return stmts, nil
}

func (f *functionSQLVertexGenerator) Delete(function schema.Function) ([]Statement, error) {
	var hazards []MigrationHazard
	if !canFunctionDependenciesBeTracked(function) {
		hazards = append(hazards, MigrationHazard{
			Type: MigrationHazardTypeHasUntrackableDependencies,
			Message: "Dependencies, i.e. other functions used in the function body, of non-sql functions cannot be " +
				"tracked. As a result, we cannot guarantee that function dependencies are ordered properly relative to " +
				"this statement. For drops, this means you need to ensure that all functions this function depends on " +
				"are dropped after this statement.",
		})
	}
	return []Statement{{
		DDL:         fmt.Sprintf("DROP FUNCTION %s", function.GetFQEscapedName()),
		Timeout:     statementTimeoutDefault,
		LockTimeout: lockTimeoutDefault,
		Hazards:     hazards,
	}}, nil
}

func (f *functionSQLVertexGenerator) Alter(diff functionDiff) ([]Statement, error) {
	// We are assuming the function has been normalized, i.e., we don't have to worry DependsOnFunctions ordering
	// causing a false positive diff detected.
	//
	// Mask everything resolved by an explicit statement below — privileges, the comment and the
	// owner — and compare the definition through its canonical form, not its text: a definition
	// read from pg_get_functiondef is not necessarily its own output (deparsing names an output
	// column a SQL-standard body left unnamed), so comparing texts would read two databases that
	// hold the same function as different. Whatever the canonical comparison leaves can only be
	// resolved by a `CREATE OR REPLACE`.
	oldMasked := diff.old
	oldMasked.Privileges = nil
	oldMasked.Description = diff.new.Description
	oldMasked.Owner = diff.new.Owner
	maskFunctionDefinition(&oldMasked.FunctionDef, &oldMasked.FunctionDefCanonical)
	newMasked := diff.new
	newMasked.Privileges = nil
	maskFunctionDefinition(&newMasked.FunctionDef, &newMasked.FunctionDefCanonical)
	replaced := !cmp.Equal(oldMasked, newMasked)

	var stmts []Statement
	if replaced {
		// Add() emits CREATE OR REPLACE plus the COMMENT and privilege statements for the new
		// schema, so the owner is masked out of it and set by an explicit statement below.
		newForAlter := diff.new
		newForAlter.Owner = ""
		addStmts, err := f.Add(newForAlter)
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, addStmts...)
	} else {
		privilegesPartialGraph, err := generatePartialGraph(
			newFunctionPrivilegeSQLVertexGenerator(diff.new.SchemaQualifiedName),
			diff.privilegesDiff,
		)
		if err != nil {
			return nil, fmt.Errorf("resolving function privilege sql: %w", err)
		}
		privilegeStmts, err := graphStatements(privilegesPartialGraph)
		if err != nil {
			return nil, fmt.Errorf("ordering function privilege sql: %w", err)
		}
		stmts = append(stmts, privilegeStmts...)
	}

	stmts = append(stmts, ownerDDLForAlter(ownershipTarget("FUNCTION", diff.new.SchemaQualifiedName), diff.old.Owner, diff.new.Owner)...)
	if replaced {
		// Add() did not emit anything when the new Description is empty, so a removed comment
		// still has to be cleared explicitly.
		if diff.new.Description == "" && diff.old.Description != "" {
			stmts = append(stmts, commentOnStatement(commentTargetFunction(diff.new.SchemaQualifiedName), ""))
		}
	} else {
		stmts = append(stmts, commentDDLForAlter(commentTargetFunction(diff.new.SchemaQualifiedName), diff.old.Description, diff.new.Description)...)
	}
	return stmts, nil
}

func canFunctionDependenciesBeTracked(function schema.Function) bool {
	return function.Language == "sql"
}

func (f *functionSQLVertexGenerator) GetSQLVertexId(function schema.Function, diffType diffType) sqlVertexId {
	return buildFunctionVertexId(function.SchemaQualifiedName, diffType)
}

func buildFunctionVertexId(name schema.SchemaQualifiedName, diffType diffType) sqlVertexId {
	return buildSchemaObjVertexId("function", name.GetFQEscapedName(), diffType)
}

// maskFunctionDefinition replaces a definition and its canonical form with the one value a diff
// compares: the canonical form, or the definition itself when the schema carries none. A fetched
// schema always carries one; a schema built in memory, which is possible only inside this module,
// does not, and is then compared by its text as before.
func maskFunctionDefinition(definition, canonical *string) {
	if *canonical == "" {
		*canonical = *definition
	}
	*definition = ""
}

// buildFunctionBareNameId identifies a function by its schema and name alone,
// dropping the argument list that EscapedName carries. PostgreSQL matches a
// function, and a `CREATE OR REPLACE`, by name and input argument types, so a
// change to the argument list or the result type appears in the diff as a delete
// of one signature and an add of another. Both are the same function to a
// reader, and the drop has to precede the create.
func buildFunctionBareNameId(name schema.SchemaQualifiedName) string {
	bare := name.EscapedName
	if i := strings.IndexByte(bare, '('); i >= 0 {
		bare = bare[:i]
	}
	return name.SchemaName + "." + bare
}

// orderFunctionDropsBeforeFunctionCreates makes the drop of a function's old
// signature run before the create of a new one with the same schema and name. A
// function whose argument list changed is a delete of the old signature and an
// add of the new one; one whose result type changed is a recreation (see
// buildSchemaDiff). In both cases PostgreSQL rejects the create while the old
// signature is still present (SQLSTATE 42P13), so the drop must be ordered first.
func orderFunctionDropsBeforeFunctionCreates(graph partialSQLGraph, diffs listDiff[schema.Function, functionDiff]) partialSQLGraph {
	deletesByBareName := make(map[string][]schema.Function)
	for _, deleted := range diffs.deletes {
		key := buildFunctionBareNameId(deleted.SchemaQualifiedName)
		deletesByBareName[key] = append(deletesByBareName[key], deleted)
	}
	for _, added := range diffs.adds {
		for _, deleted := range deletesByBareName[buildFunctionBareNameId(added.SchemaQualifiedName)] {
			graph.dependencies = append(graph.dependencies,
				mustRun(buildFunctionVertexId(deleted.SchemaQualifiedName, diffTypeDelete)).
					before(buildFunctionVertexId(added.SchemaQualifiedName, diffTypeAddAlter)))
		}
	}
	return graph
}

func (f *functionSQLVertexGenerator) GetAddAlterDependencies(newFunction, oldFunction schema.Function) ([]dependency, error) {
	// A function whose argument list or result type changed is a delete of the
	// old signature plus an add of the new one. The dependency that orders the
	// drop before the create is added at the schema level, where both the add
	// and the delete are in scope (see orderFunctionDropsBeforeFunctionCreates).
	var deps []dependency
	for _, depFunction := range newFunction.DependsOnFunctions {
		deps = append(deps, mustRun(f.GetSQLVertexId(newFunction, diffTypeAddAlter)).after(buildFunctionVertexId(depFunction, diffTypeAddAlter)))
	}
	// A `LANGUAGE sql` function's body is not ordered after the relations it reads. PostgreSQL
	// resolves those references at CREATE time but records none of them in pg_depend for a
	// string-body function, so the plan cannot see them; a SQL-standard body (`BEGIN ATOMIC`)
	// records them and does get ordered. A blanket "after every table and sequence" is harmful: it
	// closes a cycle with any table that must precede the function (a policy calling it, a domain's
	// CHECK calling it). The row type of a relation referenced by the signature — an argument type,
	// the RETURNS type, or a RETURNS TABLE column — is resolved at CREATE time, so the relation must
	// exist.
	for _, relation := range newFunction.DependsOnRelations {
		deps = append(deps, mustRun(f.GetSQLVertexId(newFunction, diffTypeAddAlter)).after(buildRelationVertexId(relation, diffTypeAddAlter)))
	}

	if !cmp.Equal(oldFunction, schema.Function{}) {
		// If the function is being altered:
		// If the old version of the function calls other functions that are being deleted come, those deletions
		// must come after the function is altered, so it is no longer dependent on those dropped functions
		for _, depFunction := range oldFunction.DependsOnFunctions {
			deps = append(deps, mustRun(f.GetSQLVertexId(newFunction, diffTypeAddAlter)).before(buildFunctionVertexId(depFunction, diffTypeDelete)))
		}
	}

	return deps, nil
}

func (f *functionSQLVertexGenerator) GetDeleteDependencies(function schema.Function) ([]dependency, error) {
	var deps []dependency
	for _, depFunction := range function.DependsOnFunctions {
		deps = append(deps, mustRun(f.GetSQLVertexId(function, diffTypeDelete)).before(buildFunctionVertexId(depFunction, diffTypeDelete)))
	}
	// A function must be dropped before a relation its signature refers to is dropped, and before
	// the relation is altered: an alteration can change the row type the signature reads, which
	// PostgreSQL refuses while the function exists.
	for _, relation := range function.DependsOnRelations {
		deps = append(deps, mustRun(f.GetSQLVertexId(function, diffTypeDelete)).before(buildRelationVertexId(relation, diffTypeDelete)))
		deps = append(deps, mustRun(f.GetSQLVertexId(function, diffTypeDelete)).before(buildRelationVertexId(relation, diffTypeAddAlter)))
	}
	return deps, nil
}
