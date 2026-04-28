package diff

import (
	"fmt"
	"strings"

	"github.com/google/go-cmp/cmp"
	"github.com/stripe/pg-schema-diff/internal/schema"
)

// compositeTypeSQLVertexGenerator handles `CREATE TYPE foo AS (...)` and
// `DROP TYPE foo` for user-defined composite types. It is a SQLVertexGenerator
// (rather than a plain SQLGenerator) so it can carry explicit ordering
// dependencies relative to the consumers of a composite type — namely tables,
// functions, procedures, and triggers, which may reference the type in column
// types, parameter types, or return types.
//
// Phase 1 scope: Add and Drop only. ALTER (when attributes change) is
// returned as ErrNotImplemented so the diff machinery fails loudly rather
// than silently dropping the change. A future phase can extend Alter to
// drop+recreate cascade for the function-only-dependents case.
type compositeTypeSQLVertexGenerator struct {
	// newSchema and oldSchema are used to set up dependency edges from every
	// table/function/procedure/trigger that may reference a composite type to
	// the type's add/delete vertices. We do not parse signatures to know
	// which functions actually reference a given composite — instead we
	// take a blanket approach and let topo-sort untangle it. This matches
	// how procedureSQLVertexGenerator handles its own untrackable deps.
	newSchema schema.Schema
	oldSchema schema.Schema
}

func newCompositeTypeSQLVertexGenerator(oldSchema, newSchema schema.Schema) sqlVertexGenerator[schema.CompositeType, compositeTypeDiff] {
	return &compositeTypeSQLVertexGenerator{
		newSchema: newSchema,
		oldSchema: oldSchema,
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

	// The type must exist before any consumer (table/function/procedure/trigger)
	// is added or altered. We add blanket "after-this" dependencies on the type
	// from every such consumer in the new schema; the topo sort will pick the
	// correct order.
	deps := c.consumerDepsForAddAlter(ct)
	// Run after re-create (if recreated). Mirrors the view/mview pattern.
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

	// The type must be dropped after every consumer (in the OLD schema) is
	// dropped or altered to no longer reference it. Add blanket
	// "before-this" dependencies on the type's delete from every consumer's
	// delete and add/alter vertices.
	deps := c.consumerDepsForDelete(ct)

	return partialSQLGraph{
		vertices: []sqlVertex{{
			id:       deleteVertexId,
			priority: sqlPriorityLater,
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
	// Comment-only diffs are emitted as a COMMENT ON TYPE statement and do
	// not require touching the type itself.
	oldCopy := d.old
	oldCopy.Description = d.new.Description
	if cmp.Equal(oldCopy, d.new) {
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

	// Anything beyond a description change requires altering the type's
	// attribute list. Phase 2 (drop dependent functions → drop type → recreate
	// type → recreate functions) is not implemented yet. For now we surface
	// ErrNotImplemented so the user gets an explicit error rather than a
	// silent drop.
	return partialSQLGraph{}, fmt.Errorf("altering composite type attributes: %w", ErrNotImplemented)
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
		def := fmt.Sprintf("\t%s %s", schema.EscapeIdentifier(a.Name), a.Type)
		if !a.Collation.IsEmpty() {
			def += fmt.Sprintf(" COLLATE %s", a.Collation.GetFQEscapedName())
		}
		attrDefs = append(attrDefs, def)
	}
	return fmt.Sprintf("CREATE TYPE %s AS (\n%s\n)", ct.GetFQEscapedName(), strings.Join(attrDefs, ",\n"))
}

// consumerDepsForAddAlter returns dependency edges that force the
// composite type's CREATE to run before any consumer's CREATE/ALTER in
// the new schema.
func (c *compositeTypeSQLVertexGenerator) consumerDepsForAddAlter(ct schema.CompositeType) []dependency {
	addVertexId := buildCompositeTypeVertexId(ct.SchemaQualifiedName, diffTypeAddAlter)

	var deps []dependency
	for _, t := range c.newSchema.Tables {
		deps = append(deps, mustRun(addVertexId).before(buildTableVertexId(t.SchemaQualifiedName, diffTypeAddAlter)))
	}
	for _, f := range c.newSchema.Functions {
		deps = append(deps, mustRun(addVertexId).before(buildFunctionVertexId(f.SchemaQualifiedName, diffTypeAddAlter)))
	}
	for _, p := range c.newSchema.Procedures {
		deps = append(deps, mustRun(addVertexId).before(buildProcedureVertexId(p.SchemaQualifiedName, diffTypeAddAlter)))
	}
	return deps
}

// consumerDepsForDelete returns dependency edges that force the
// composite type's DROP to run after every consumer in the old schema is
// dropped or altered (so consumers no longer reference the type).
func (c *compositeTypeSQLVertexGenerator) consumerDepsForDelete(ct schema.CompositeType) []dependency {
	deleteVertexId := buildCompositeTypeVertexId(ct.SchemaQualifiedName, diffTypeDelete)

	var deps []dependency
	for _, t := range c.oldSchema.Tables {
		deps = append(deps, mustRun(deleteVertexId).after(buildTableVertexId(t.SchemaQualifiedName, diffTypeDelete)))
		deps = append(deps, mustRun(deleteVertexId).after(buildTableVertexId(t.SchemaQualifiedName, diffTypeAddAlter)))
	}
	for _, f := range c.oldSchema.Functions {
		deps = append(deps, mustRun(deleteVertexId).after(buildFunctionVertexId(f.SchemaQualifiedName, diffTypeDelete)))
		deps = append(deps, mustRun(deleteVertexId).after(buildFunctionVertexId(f.SchemaQualifiedName, diffTypeAddAlter)))
	}
	for _, p := range c.oldSchema.Procedures {
		deps = append(deps, mustRun(deleteVertexId).after(buildProcedureVertexId(p.SchemaQualifiedName, diffTypeDelete)))
		deps = append(deps, mustRun(deleteVertexId).after(buildProcedureVertexId(p.SchemaQualifiedName, diffTypeAddAlter)))
	}
	return deps
}
