package diff

import (
	"fmt"

	"github.com/google/go-cmp/cmp"
	"github.com/stripe/pg-schema-diff/internal/schema"
)

type procedureSQLVertexGenerator struct {
	newSchema schema.Schema
}

func newProcedureSqlVertexGenerator(newSchema schema.Schema) sqlVertexGenerator[schema.Procedure, procedureDiff] {
	return &procedureSQLVertexGenerator{
		newSchema: newSchema,
	}
}

func (p procedureSQLVertexGenerator) Add(s schema.Procedure) (partialSQLGraph, error) {
	// Procedures can't be added until all dependencies have been added. Weirdly, Postgres ONLY enforces these
	// dependencies at creation time and not after...so we will make a best effort to order this statement after
	// all other dependencies that procedures might depend on.

	var deps []dependency

	// Run after all tables have been added/altered, since a procedure might query a table.
	for _, t := range p.newSchema.Tables {
		deps = append(deps, mustRun(buildProcedureVertexId(s.SchemaQualifiedName, diffTypeAddAlter)).after(buildTableVertexId(t.SchemaQualifiedName, diffTypeAddAlter)))
	}

	// Run after all functions, since a procedure might call a function.
	for _, f := range p.newSchema.Functions {
		deps = append(deps, mustRun(buildProcedureVertexId(s.SchemaQualifiedName, diffTypeAddAlter)).after(buildFunctionVertexId(f.SchemaQualifiedName, diffTypeAddAlter)))
	}

	// Run after all sequences, since a procedure might call a sequence.
	for _, seq := range p.newSchema.Sequences {
		deps = append(deps, mustRun(buildProcedureVertexId(s.SchemaQualifiedName, diffTypeAddAlter)).after(buildSequenceVertexId(seq.SchemaQualifiedName, diffTypeAddAlter)))
	}

	stmts := []Statement{{
		DDL:         s.Def,
		Timeout:     statementTimeoutDefault,
		LockTimeout: lockTimeoutDefault,
		Hazards: []MigrationHazard{{
			Type: MigrationHazardTypeHasUntrackableDependencies,
			Message: "Dependencies of procedures are not tracked by Postgres. " +
				"As a result, we cannot guarantee that this procedure's dependencies are ordered properly relative to " +
				"this statement. For adds, this means you need to ensure that all objects this function depends on " +
				"are added before this statement.",
		}},
	}}
	stmts = append(stmts, ownerDDLForAdd(ownershipTarget("PROCEDURE", s.SchemaQualifiedName), s.Owner)...)
	stmts = append(stmts, commentDDLForAdd(commentTargetProcedure(s.SchemaQualifiedName), s.Description)...)
	privilegeStmts, err := exactRoutinePrivilegeStatements(
		newProcedurePrivilegeSQLVertexGenerator(s.SchemaQualifiedName),
		s.Privileges,
	)
	if err != nil {
		return partialSQLGraph{}, fmt.Errorf("generating procedure privilege statements: %w", err)
	}
	stmts = append(stmts, privilegeStmts...)

	return partialSQLGraph{
		vertices: []sqlVertex{{
			id:         buildProcedureVertexId(s.SchemaQualifiedName, diffTypeAddAlter),
			priority:   sqlPrioritySooner,
			statements: stmts,
		}},
		dependencies: deps,
	}, nil
}

func (p procedureSQLVertexGenerator) Delete(s schema.Procedure) (partialSQLGraph, error) {
	// Stored procedure dependencies can't be tracked...so they can either be deleted earlier or later. We will
	// delete earlier, since a procedure is more likely to depend on objects that being depended on. Thus, we will have
	// a stored procedure drop before other objects that might depend on it.
	var deps []dependency

	// Run before all tables have been added/altered, since a procedure might query a table. This does not work for columns
	// being dropped because column drops are not "trackable" from external SQL generators until
	// https://github.com/stripe/pg-schema-diff/issues/131 is fully implemented.
	for _, t := range p.newSchema.Tables {
		deps = append(deps, mustRun(buildProcedureVertexId(s.SchemaQualifiedName, diffTypeDelete)).after(buildTableVertexId(t.SchemaQualifiedName, diffTypeAddAlter)))
	}

	// Run before all functions, since a procedure might call a function.
	for _, f := range p.newSchema.Functions {
		deps = append(deps, mustRun(buildProcedureVertexId(s.SchemaQualifiedName, diffTypeDelete)).after(buildFunctionVertexId(f.SchemaQualifiedName, diffTypeAddAlter)))
	}

	// Run before all sequences, since a procedure might call a sequence.
	for _, seq := range p.newSchema.Sequences {
		deps = append(deps, mustRun(buildProcedureVertexId(s.SchemaQualifiedName, diffTypeDelete)).after(buildSequenceVertexId(seq.SchemaQualifiedName, diffTypeAddAlter)))
	}

	return partialSQLGraph{
		vertices: []sqlVertex{{
			id:       buildProcedureVertexId(s.SchemaQualifiedName, diffTypeDelete),
			priority: sqlPriorityLater,
			statements: []Statement{{
				DDL:         fmt.Sprintf("DROP PROCEDURE %s", s.GetFQEscapedName()),
				Timeout:     statementTimeoutDefault,
				LockTimeout: lockTimeoutDefault,
				Hazards: []MigrationHazard{{
					Type: MigrationHazardTypeHasUntrackableDependencies,
					Message: "Dependencies of procedures are not tracked by Postgres. " +
						"As a result, we cannot guarantee that this procedure's dependencies are ordered properly relative to " +
						"this statement. For drops, this means you need to ensure that all objects this function depends on " +
						"are dropped after this statement.",
				}},
			}},
		}},
		dependencies: deps,
	}, nil
}

func (p procedureSQLVertexGenerator) Alter(d procedureDiff) (partialSQLGraph, error) {
	// Mask everything resolved by an explicit statement below — privileges, the comment and the
	// owner. Whatever remains can only be resolved by re-creating the procedure.
	oldMasked := d.old
	oldMasked.Privileges = nil
	oldMasked.Description = d.new.Description
	oldMasked.Owner = d.new.Owner
	newMasked := d.new
	newMasked.Privileges = nil
	recreated := !cmp.Equal(oldMasked, newMasked)

	var partialGraph partialSQLGraph
	if recreated {
		// Add() also re-emits the COMMENT and privilege statements for the new schema, so the
		// owner is masked out of it and set by an explicit statement below.
		newForAlter := d.new
		newForAlter.Owner = ""
		addPartialGraph, err := p.Add(newForAlter)
		if err != nil {
			return partialSQLGraph{}, err
		}
		partialGraph = concatPartialGraphs(partialGraph, addPartialGraph)
	}

	metadataStmts := ownerDDLForAlter(ownershipTarget("PROCEDURE", d.new.SchemaQualifiedName), d.old.Owner, d.new.Owner)
	if recreated {
		// Add() did not emit anything when the new Description is empty, so a removed comment
		// still has to be cleared explicitly.
		if d.new.Description == "" && d.old.Description != "" {
			metadataStmts = append(metadataStmts, commentOnStatement(commentTargetProcedure(d.new.SchemaQualifiedName), ""))
		}
	} else {
		metadataStmts = append(metadataStmts, commentDDLForAlter(commentTargetProcedure(d.new.SchemaQualifiedName), d.old.Description, d.new.Description)...)
	}
	if len(metadataStmts) > 0 {
		partialGraph = concatPartialGraphs(partialGraph, partialSQLGraph{
			vertices: []sqlVertex{{
				id:         buildProcedureVertexId(d.new.SchemaQualifiedName, diffTypeAddAlter),
				priority:   sqlPrioritySooner,
				statements: metadataStmts,
			}},
		})
	}

	privilegesPartialGraph, err := generatePartialGraph(
		newProcedurePrivilegeSQLVertexGenerator(d.new.SchemaQualifiedName),
		d.privilegesDiff,
	)
	if err != nil {
		return partialSQLGraph{}, fmt.Errorf("resolving procedure privilege sql: %w", err)
	}
	return concatPartialGraphs(partialGraph, privilegesPartialGraph), nil
}

func buildProcedureVertexId(name schema.SchemaQualifiedName, diffType diffType) sqlVertexId {
	return buildSchemaObjVertexId("procedure", name.GetFQEscapedName(), diffType)
}
