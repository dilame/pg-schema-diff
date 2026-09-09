package diff

import (
	"fmt"

	"github.com/google/go-cmp/cmp"
	"github.com/stripe/pg-schema-diff/internal/schema"
)

type functionSQLVertexGenerator struct {
	// functionsInNewSchemaByName is a map of function name to functions in the new schema.
	// These functions are not necessarily new
	functionsInNewSchemaByName map[string]schema.Function
	newSchema                  schema.Schema
}

func newFunctionSqlVertexGenerator(functionsInNewSchemaByName map[string]schema.Function, newSchema schema.Schema) sqlVertexGenerator[schema.Function, functionDiff] {
	return legacyToNewSqlVertexGenerator[schema.Function, functionDiff](&functionSQLVertexGenerator{
		functionsInNewSchemaByName: functionsInNewSchemaByName,
		newSchema:                  newSchema,
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
	// owner. Whatever remains can only be resolved by a `CREATE OR REPLACE`.
	oldMasked := diff.old
	oldMasked.Privileges = nil
	oldMasked.Description = diff.new.Description
	oldMasked.Owner = diff.new.Owner
	newMasked := diff.new
	newMasked.Privileges = nil
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

func (f *functionSQLVertexGenerator) GetAddAlterDependencies(newFunction, oldFunction schema.Function) ([]dependency, error) {
	// Since functions can just be `CREATE OR REPLACE`, there will never be a case where a function is
	// added and dropped in the same migration. Thus, we don't need a dependency on the delete vertex of a function
	// because there won't be one if it is being added/altered
	var deps []dependency
	for _, depFunction := range newFunction.DependsOnFunctions {
		deps = append(deps, mustRun(f.GetSQLVertexId(newFunction, diffTypeAddAlter)).after(buildFunctionVertexId(depFunction, diffTypeAddAlter)))
	}
	if canFunctionDependenciesBeTracked(newFunction) {
		deps = append(deps, f.getRelationAddAlterDependencies(newFunction)...)
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
	return deps, nil
}

func (f *functionSQLVertexGenerator) getRelationAddAlterDependencies(function schema.Function) []dependency {
	var deps []dependency

	// SQL functions validate table and sequence references in their body at
	// CREATE time, but PostgreSQL does not expose those body relation references
	// as pg_proc -> pg_class dependencies in pg_depend. Keep this deliberately
	// broad, mirroring the procedure generator's best-effort relation ordering.
	for _, table := range f.newSchema.Tables {
		deps = append(deps, mustRun(f.GetSQLVertexId(function, diffTypeAddAlter)).after(buildTableVertexId(table.SchemaQualifiedName, diffTypeAddAlter)))
	}
	for _, seq := range f.newSchema.Sequences {
		deps = append(deps, mustRun(f.GetSQLVertexId(function, diffTypeAddAlter)).after(buildSequenceVertexId(seq.SchemaQualifiedName, diffTypeAddAlter)))
	}

	return deps
}
