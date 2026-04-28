package migration_acceptance_tests

import (
	"testing"

	"github.com/stripe/pg-schema-diff/pkg/diff"
)

var compositeTypeAcceptanceTestCases = []acceptanceTestCase{
	{
		name: "no-op",
		oldSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text);
		`},
		newSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text);
		`},
		expectEmptyPlan: true,
	},
	{
		name:         "create composite type",
		oldSchemaDDL: []string{},
		newSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text);
		`},
	},
	{
		name: "drop composite type",
		oldSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text);
		`},
		newSchemaDDL: []string{},
	},
	{
		name:         "create composite type with comment",
		oldSchemaDDL: []string{},
		newSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text);
			COMMENT ON TYPE pair IS 'pair of values';
		`},
	},
	{
		name: "add comment to composite type",
		oldSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text);
		`},
		newSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text);
			COMMENT ON TYPE pair IS 'pair of values';
		`},
	},
	{
		name: "change composite type comment",
		oldSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text);
			COMMENT ON TYPE pair IS 'old';
		`},
		newSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text);
			COMMENT ON TYPE pair IS 'new';
		`},
	},
	{
		name: "remove composite type comment",
		oldSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text);
			COMMENT ON TYPE pair IS 'old';
		`},
		newSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text);
		`},
	},
	{
		name:         "create composite type used by function",
		oldSchemaDDL: []string{},
		newSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text);
			CREATE FUNCTION mk_pair(x int, y text) RETURNS pair LANGUAGE sql AS 'SELECT (x, y)::pair';
		`},
	},
	{
		name: "drop composite type after dropping function that used it",
		oldSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text);
			CREATE FUNCTION mk_pair(x int, y text) RETURNS pair LANGUAGE sql AS 'SELECT (x, y)::pair';
		`},
		newSchemaDDL: []string{},
	},
	{
		name:         "create composite type with attributes that have collation",
		oldSchemaDDL: []string{},
		newSchemaDDL: []string{`
			CREATE TYPE labelled AS (id int, label text COLLATE "C");
		`},
	},
	{
		name:         "create nested composite types (one references the other)",
		oldSchemaDDL: []string{},
		newSchemaDDL: []string{`
			CREATE TYPE inner_t AS (n int);
			CREATE TYPE outer_t AS (i inner_t, label text);
		`},
	},
	{
		name: "alter composite type attributes is unsupported",
		oldSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text);
		`},
		newSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text, c boolean);
		`},
		expectedPlanErrorIs: diff.ErrNotImplemented,
	},
}

func TestCompositeTypeTestCases(t *testing.T) {
	runTestCases(t, compositeTypeAcceptanceTestCases)
}
