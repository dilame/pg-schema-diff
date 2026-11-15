package diff

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stripe/pg-schema-diff/internal/schema"
)

// A CREATE OR REPLACE keeps the function's ACL, so the privilege statements of a replaced
// function are the difference between the old and the new ACL. Emitting the new ACL as for a
// new object grants the target's roles but never takes away a role the target no longer declares,
// and the applied plan stops short of the target schema.
func TestFunctionAlterRevokesGrantTheTargetDrops(t *testing.T) {
	name := schema.SchemaQualifiedName{
		SchemaName:  "public",
		EscapedName: "execute_for_roles()",
	}
	oldFunction := schema.Function{
		SchemaQualifiedName: name,
		FunctionDef:         `CREATE OR REPLACE FUNCTION public.execute_for_roles() RETURNS integer LANGUAGE sql AS $$ SELECT 1 $$`,
		Language:            "sql",
		Privileges: []schema.Privilege{
			{Privilege: "EXECUTE"},
			{Grantee: "reader", Privilege: "EXECUTE"},
			{Grantee: "server", Privilege: "EXECUTE"},
		},
	}
	newFunction := oldFunction
	newFunction.FunctionDef = `CREATE OR REPLACE FUNCTION public.execute_for_roles() RETURNS integer LANGUAGE sql AS $$ SELECT 2 $$`
	newFunction.Privileges = []schema.Privilege{
		{Privilege: "EXECUTE"},
		{Grantee: "server", Privilege: "EXECUTE"},
	}

	privilegesDiff, err := buildPrivilegeDiffs(oldFunction.Privileges, newFunction.Privileges)
	require.NoError(t, err)
	stmts, err := (&functionSQLVertexGenerator{}).Alter(functionDiff{
		oldAndNew:      oldAndNew[schema.Function]{old: oldFunction, new: newFunction},
		privilegesDiff: privilegesDiff,
	})
	require.NoError(t, err)

	var ddl []string
	for _, stmt := range stmts {
		ddl = append(ddl, stmt.DDL)
	}
	assert.Contains(t, ddl, `REVOKE EXECUTE ON FUNCTION "public".execute_for_roles() FROM "reader"`)
}
