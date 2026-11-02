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
		name: "drop nested composite types",
		oldSchemaDDL: []string{`
			CREATE TYPE inner_t AS (n int);
			CREATE TYPE outer_t AS (i inner_t, label text);
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
		name:         "create schema-qualified composite type used by plpgsql function",
		oldSchemaDDL: []string{},
		newSchemaDDL: []string{`
			CREATE SCHEMA app;
			CREATE TYPE app.result AS (status text, reason text);
			CREATE FUNCTION app.resolve() RETURNS app.result LANGUAGE plpgsql AS $$
			DECLARE
				v_result app.result;
			BEGIN
				SELECT ROW('ok', 'ready')::app.result INTO v_result;
				RETURN v_result;
			END
			$$;
		`},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeHasUntrackableDependencies,
		},
	},
	{
		name:         "create composite types before functions that use them in signatures",
		oldSchemaDDL: []string{},
		newSchemaDDL: []string{`
			CREATE SCHEMA casino_wager_stats;
			CREATE TYPE casino_wager_stats.peak_source_row AS (
				id bigint,
				round_id bigint
			);
			CREATE TYPE casino_wager_stats.peak_candidate_row AS (
				metric_code text,
				round_id bigint
			);
			CREATE FUNCTION casino_wager_stats.project_peak_candidates(p_peak_sources casino_wager_stats.peak_source_row[])
				RETURNS SETOF casino_wager_stats.peak_candidate_row
				LANGUAGE sql
				STABLE
				AS 'SELECT ''payout''::text AS metric_code, source$.round_id FROM pg_catalog.unnest(p_peak_sources) AS source$';
			CREATE FUNCTION casino_wager_stats.refresh_peaks(p_peak_sources casino_wager_stats.peak_source_row[])
				RETURNS void
				LANGUAGE plpgsql
				AS $$
				DECLARE
					v_candidates casino_wager_stats.peak_candidate_row[];
				BEGIN
					SELECT COALESCE(array_agg(candidate$), ARRAY[]::casino_wager_stats.peak_candidate_row[])
					INTO v_candidates
					FROM casino_wager_stats.project_peak_candidates(p_peak_sources) candidate$;
				END;
				$$;
		`},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeHasUntrackableDependencies,
		},
		expectedPlanDDL: []string{
			`CREATE SCHEMA "casino_wager_stats" AUTHORIZATION "postgres"`,
			`CREATE TYPE "casino_wager_stats"."peak_candidate_row" AS (
	"metric_code" text COLLATE "pg_catalog"."default",
	"round_id" bigint
)`,
			`CREATE TYPE "casino_wager_stats"."peak_source_row" AS (
	"id" bigint,
	"round_id" bigint
)`,
			"CREATE OR REPLACE FUNCTION casino_wager_stats.project_peak_candidates(p_peak_sources casino_wager_stats.peak_source_row[])\n RETURNS SETOF casino_wager_stats.peak_candidate_row\n LANGUAGE sql\n STABLE\nAS $function$SELECT 'payout'::text AS metric_code, source$.round_id FROM pg_catalog.unnest(p_peak_sources) AS source$$function$\n",
			`ALTER FUNCTION "casino_wager_stats"."project_peak_candidates"(p_peak_sources casino_wager_stats.peak_source_row[]) OWNER TO "postgres"`,
			"CREATE OR REPLACE FUNCTION casino_wager_stats.refresh_peaks(p_peak_sources casino_wager_stats.peak_source_row[])\n RETURNS void\n LANGUAGE plpgsql\nAS $function$\n\t\t\t\tDECLARE\n\t\t\t\t\tv_candidates casino_wager_stats.peak_candidate_row[];\n\t\t\t\tBEGIN\n\t\t\t\t\tSELECT COALESCE(array_agg(candidate$), ARRAY[]::casino_wager_stats.peak_candidate_row[])\n\t\t\t\t\tINTO v_candidates\n\t\t\t\t\tFROM casino_wager_stats.project_peak_candidates(p_peak_sources) candidate$;\n\t\t\t\tEND;\n\t\t\t\t$function$\n",
			`ALTER FUNCTION "casino_wager_stats"."refresh_peaks"(p_peak_sources casino_wager_stats.peak_source_row[]) OWNER TO "postgres"`,
		},
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
	// ─── Phase 2: drop+recreate cascade for function-only dependents ───
	{
		name: "alter composite type attrs - cascade through dependent function",
		oldSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text);
			CREATE FUNCTION mk_pair(x int, y text) RETURNS pair LANGUAGE sql AS 'SELECT (x, y)::pair';
		`},
		newSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text, c boolean);
			CREATE FUNCTION mk_pair(x int, y text, z boolean) RETURNS pair LANGUAGE sql AS 'SELECT (x, y, z)::pair';
		`},
	},
	{
		name: "alter composite type attrs in place - append attribute with dependent procedure",
		oldSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text);
			CREATE PROCEDURE use_pair(p pair) LANGUAGE plpgsql AS $$ BEGIN END $$;
		`},
		newSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text, c boolean);
			CREATE PROCEDURE use_pair(p pair) LANGUAGE plpgsql AS $$ BEGIN END $$;
		`},
		// Appending an attribute keeps the type's OID, so the procedure is not touched.
		expectedPlanDDL: []string{
			`ALTER TYPE "public"."pair" ADD ATTRIBUTE "c" boolean`,
		},
	},
	{
		name: "alter composite type attrs - cascade through multiple dependent functions",
		oldSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text);
			CREATE FUNCTION f_a(p pair) RETURNS int LANGUAGE sql AS 'SELECT (p).a';
			CREATE FUNCTION f_b(p pair) RETURNS text LANGUAGE sql AS 'SELECT (p).b';
		`},
		newSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text, c boolean);
			CREATE FUNCTION f_a(p pair) RETURNS int LANGUAGE sql AS 'SELECT (p).a';
			CREATE FUNCTION f_b(p pair) RETURNS text LANGUAGE sql AS 'SELECT (p).b';
		`},
	},
	{
		name: "alter composite type attrs - cascade through dependent function using array argument",
		oldSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text);
			CREATE FUNCTION f_items(p pair[]) RETURNS int LANGUAGE sql AS 'SELECT pg_catalog.cardinality(p)';
		`},
		newSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text, c boolean);
			CREATE FUNCTION f_items(p pair[]) RETURNS int LANGUAGE sql AS 'SELECT pg_catalog.cardinality(p)';
		`},
	},
	{
		name: "alter composite type attrs - cascade through dependent composite type and function",
		oldSchemaDDL: []string{`
			CREATE TYPE inner_t AS (n int);
			CREATE TYPE outer_t AS (i inner_t, label text);
			CREATE FUNCTION f_outer(p outer_t) RETURNS int LANGUAGE sql AS 'SELECT ((p).i).n';
		`},
		newSchemaDDL: []string{`
			CREATE TYPE inner_t AS (n int, extra text);
			CREATE TYPE outer_t AS (i inner_t, label text);
			CREATE FUNCTION f_outer(p outer_t) RETURNS int LANGUAGE sql AS 'SELECT ((p).i).n';
		`},
	},
	{
		name: "alter composite type attrs is unsupported when used by a table column",
		oldSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text);
			CREATE TABLE users (id int, attrs pair);
		`},
		newSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text, c boolean);
			CREATE TABLE users (id int, attrs pair);
		`},
		expectedPlanErrorIs: diff.ErrNotImplemented,
	},
	{
		name: "alter composite type attrs in place through dependent composite type used by a table column",
		oldSchemaDDL: []string{`
			CREATE TYPE inner_t AS (n int);
			CREATE TYPE outer_t AS (i inner_t, label text);
			CREATE TABLE users (id int, attrs outer_t);
		`},
		newSchemaDDL: []string{`
			CREATE TYPE inner_t AS (n int, extra text);
			CREATE TYPE outer_t AS (i inner_t, label text);
			CREATE TABLE users (id int, attrs outer_t);
		`},
		// The appended attribute keeps inner_t's OID, so outer_t and the table column
		// that uses it are untouched.
		expectedPlanDDL: []string{
			`ALTER TYPE "public"."inner_t" ADD ATTRIBUTE "extra" text COLLATE "pg_catalog"."default"`,
		},
	},
	{
		name: "alter composite type attrs is unsupported when used by a table array column",
		oldSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text);
			CREATE TABLE users (id int, attrs pair[]);
		`},
		newSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text, c boolean);
			CREATE TABLE users (id int, attrs pair[]);
		`},
		expectedPlanErrorIs: diff.ErrNotImplemented,
	},
	// ─── Re-creating a type whose layout ALTER cannot produce ───
	{
		name: "recreate composite type with dependent view and function when an attribute is inserted",
		oldSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text);
			CREATE VIEW pair_a AS SELECT (ROW(1, 'x')::pair).a AS a;
			CREATE FUNCTION pair_a_value(p pair) RETURNS int LANGUAGE sql AS 'SELECT (p).a';
			CREATE TABLE unrelated (id int);
		`},
		newSchemaDDL: []string{`
			CREATE TYPE pair AS (c boolean, a int, b text);
			CREATE VIEW pair_a AS SELECT (ROW(true, 1, 'x')::pair).a AS a;
			CREATE FUNCTION pair_a_value(p pair) RETURNS int LANGUAGE sql AS 'SELECT (p).a';
			CREATE TABLE unrelated (id int);
		`},
	},
	{
		name: "recreate nested composite type with dependent view and unrelated relations present",
		oldSchemaDDL: []string{`
			CREATE TYPE inner_t AS (n int);
			CREATE TYPE outer_t AS (i inner_t, label text);
			CREATE VIEW outer_label AS SELECT (ROW(ROW(1)::inner_t, 'x')::outer_t).label AS label;
			CREATE FUNCTION unrelated_fn() RETURNS void LANGUAGE sql AS 'SELECT NULL';
			CREATE TABLE unrelated (id int);
		`},
		newSchemaDDL: []string{`
			CREATE TYPE inner_t AS (extra text, n int);
			CREATE TYPE outer_t AS (i inner_t, label text);
			CREATE VIEW outer_label AS SELECT (ROW(ROW('y', 1)::inner_t, 'x')::outer_t).label AS label;
			CREATE FUNCTION unrelated_fn() RETURNS void LANGUAGE sql AS 'SELECT NULL';
			CREATE TABLE unrelated (id int);
		`},
	},
	// ─── Altering a type in place ───
	{
		name: "recreate composite type when a trailing attribute is dropped with a dependent view and function",
		oldSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text);
			CREATE VIEW pair_a AS SELECT (ROW(1, 'x')::pair).a AS a;
			CREATE FUNCTION pair_a_value(p pair) RETURNS int LANGUAGE sql AS 'SELECT (p).a';
		`},
		newSchemaDDL: []string{`
			CREATE TYPE pair AS (a int);
			CREATE VIEW pair_a AS SELECT (ROW(1)::pair).a AS a;
			CREATE FUNCTION pair_a_value(p pair) RETURNS int LANGUAGE sql AS 'SELECT (p).a';
		`},
	},
	{
		name: "recreate composite type when an attribute is retyped with a dependent function",
		oldSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text);
			CREATE FUNCTION pair_a(p pair) RETURNS int LANGUAGE sql AS 'SELECT (p).a';
		`},
		newSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b character varying);
			CREATE FUNCTION pair_a(p pair) RETURNS int LANGUAGE sql AS 'SELECT (p).a';
		`},
	},
	{
		name: "alter composite type attrs in place - append attribute read by a new view",
		oldSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text);
		`},
		newSchemaDDL: []string{`
			CREATE TYPE pair AS (a int, b text, c boolean);
			CREATE VIEW pair_c AS SELECT (ROW(1, 'x', true)::pair).c AS c;
		`},
	},
}

func TestCompositeTypeTestCases(t *testing.T) {
	runTestCases(t, compositeTypeAcceptanceTestCases)
}
