package migration_acceptance_tests

import (
	"testing"

	"github.com/stripe/pg-schema-diff/pkg/diff"
)

var functionAcceptanceTestCases = []acceptanceTestCase{
	{
		name: "No-op",
		oldSchemaDDL: []string{
			`
            CREATE FUNCTION add(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN a + b;

            CREATE OR REPLACE FUNCTION increment(i integer) RETURNS integer AS $$
                    BEGIN
                            RETURN i + 1;
                    END;
            $$ LANGUAGE plpgsql;
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE FUNCTION add(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN a + b;

            CREATE OR REPLACE FUNCTION increment(i integer) RETURNS integer AS $$
                    BEGIN
                            RETURN i + 1;
                    END;
            $$ LANGUAGE plpgsql;
			`,
		},

		expectEmptyPlan: true,
	},
	{
		name:         "Create functions (with conflicting names)",
		oldSchemaDDL: nil,
		newSchemaDDL: []string{
			`
            CREATE FUNCTION add(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN a + b;
            CREATE FUNCTION add(a text, b text) RETURNS text
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN CONCAT(a, b);

            CREATE SCHEMA schema_1;
            CREATE FUNCTION schema_1.add(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN a + b;
            CREATE FUNCTION schema_1.add(a text, b text) RETURNS text
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN CONCAT(a, b);
			`,
		},
	},
	{
		name:         "Create sql function after table referenced by body",
		oldSchemaDDL: nil,
		newSchemaDDL: []string{
			`
			CREATE SCHEMA app;
			CREATE TABLE app.dead_letter(id int);
			CREATE FUNCTION app.record_dead_letter()
			RETURNS void
			LANGUAGE sql
			AS $$ INSERT INTO app.dead_letter(id) VALUES (1) $$;
			`,
		},
	},
	{
		name:         "Create functions with quoted names (with conflicting names)",
		oldSchemaDDL: nil,
		newSchemaDDL: []string{
			`
            CREATE FUNCTION "some add"(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN a + b;
            CREATE FUNCTION "some add"(a text, b text) RETURNS text
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN CONCAT(a, b);
			`,
		},
	},
	{
		name:         "Create non-sql function",
		oldSchemaDDL: nil,
		newSchemaDDL: []string{
			`
            CREATE FUNCTION non_sql_func(i integer) RETURNS integer AS $$
                    BEGIN
                            RETURN i + 1;
                    END;
            $$ LANGUAGE plpgsql;
		`},
		expectedHazardTypes: []diff.MigrationHazardType{diff.MigrationHazardTypeHasUntrackableDependencies},
	},
	{
		name:         "Create function with dependencies",
		oldSchemaDDL: nil,
		newSchemaDDL: []string{
			`
            CREATE SCHEMA schema_1;
            CREATE FUNCTION schema_1.add(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN a + b;

            CREATE FUNCTION "increment func"(i integer) RETURNS integer AS $$
                    BEGIN
                            RETURN i + 1;
                    END;
            $$ LANGUAGE plpgsql;

            CREATE FUNCTION function_with_dependencies(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN schema_1.add(a, b) + "increment func"(a);

            -- function with conflicting name to ensure the deps specify param name
            CREATE FUNCTION add(a text, b text) RETURNS text
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN CONCAT(a, b);

            -- identical function on a different schema
            CREATE SCHEMA schema_2;
            CREATE FUNCTION schema_2.add(a integer, b integer) RETURNS text
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN CONCAT(a, b);
		`},
		expectedHazardTypes: []diff.MigrationHazardType{diff.MigrationHazardTypeHasUntrackableDependencies},
	},
	{
		name: "Create function with an extension that also creates functions installed",
		oldSchemaDDL: []string{
			`
            CREATE EXTENSION amcheck;
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE EXTENSION amcheck;

            CREATE FUNCTION add(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN a + b;
			`,
		},
	},
	{
		name: "Drop functions (with conflicting names)",
		oldSchemaDDL: []string{
			`
            CREATE FUNCTION add(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN a + b;
            CREATE FUNCTION add(a text, b text) RETURNS text
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN CONCAT(a, b);
			`,
		},
		newSchemaDDL: nil,
	},
	{
		name: "Drop functions with quoted names (with conflicting names)",
		oldSchemaDDL: []string{
			`
            CREATE FUNCTION "some add"(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN a + b;
            CREATE FUNCTION "some add"(a text, b text) RETURNS text
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN CONCAT(a, b);
			`,
		},
		newSchemaDDL: nil,
	},
	{
		name: "Drop non-sql function",
		oldSchemaDDL: []string{
			`
            CREATE FUNCTION non_sql_func(i integer) RETURNS integer AS $$
                    BEGIN
                            RETURN i + 1;
                    END;
            $$ LANGUAGE plpgsql;
		`},
		newSchemaDDL:        nil,
		expectedHazardTypes: []diff.MigrationHazardType{diff.MigrationHazardTypeHasUntrackableDependencies},
	},
	{
		name: "Drop function with dependencies",
		oldSchemaDDL: []string{
			`
            CREATE SCHEMA schema_1;
            CREATE FUNCTION schema_1.add(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN a + b;

            CREATE FUNCTION "increment func"(i integer) RETURNS integer AS $$
                    BEGIN
                            RETURN i + 1;
                    END;
            $$ LANGUAGE plpgsql;

            CREATE FUNCTION function_with_dependencies(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN schema_1.add(a, b) + "increment func"(a);

            -- function with conflicting name to ensure the deps specify param name
            CREATE FUNCTION add(a text, b text) RETURNS text
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN CONCAT(a, b);

            -- identical function on a different schema to ensure schemas are specified correctly
            CREATE SCHEMA schema_2;
            CREATE FUNCTION schema_2.add(a integer, b integer) RETURNS text
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN CONCAT(a, b);
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{diff.MigrationHazardTypeHasUntrackableDependencies},
	},
	{
		name: "Add and drop functions with dependencies (conflicting schemas)",
		oldSchemaDDL: []string{
			`
            CREATE SCHEMA schema_1;
            CREATE FUNCTION schema_1.add(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN a + b;

            CREATE FUNCTION schema_1."increment func"(i integer) RETURNS integer AS $$
                    BEGIN
                            RETURN i + 1;
                    END;
            $$ LANGUAGE plpgsql;

            CREATE FUNCTION schema_1.function_with_dependencies(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN schema_1.add(a, b) + schema_1."increment func"(a);
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE SCHEMA schema_2;
            CREATE FUNCTION schema_2.add(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN a + b;

            CREATE FUNCTION schema_2."increment func"(i integer) RETURNS integer AS $$
                    BEGIN
                            RETURN i + 1;
                    END;
            $$ LANGUAGE plpgsql;

            CREATE FUNCTION schema_2.function_with_dependencies(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN schema_2.add(a, b) + schema_2."increment func"(a);
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{diff.MigrationHazardTypeHasUntrackableDependencies},
	},
	{
		name: "Alter functions (with conflicting names)",
		oldSchemaDDL: []string{
			`
            CREATE FUNCTION add(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN a + b;
            CREATE FUNCTION add(a TEXT, b TEXT) RETURNS TEXT
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN CONCAT(a, b);
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE FUNCTION add(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN a + a + b;
            CREATE FUNCTION add(a TEXT, b TEXT) RETURNS TEXT
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN CONCAT(CONCAT(a, a), b);
			`,
		},
	},
	{
		name: "Alter functions with quoted names (with conflicting names)",
		oldSchemaDDL: []string{
			`
            CREATE FUNCTION "some add"(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN a + b;
            CREATE FUNCTION "some add"(a TEXT, b TEXT) RETURNS TEXT
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN CONCAT(a, b);
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE FUNCTION "some add"(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN a + a + b;
            CREATE FUNCTION "some add"(a TEXT, b TEXT) RETURNS TEXT
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN CONCAT(CONCAT(a, a), b);
			`,
		},
	},
	{
		name: "Alter function argument list (OUT parameter renamed) drops the old signature before creating the new one",
		oldSchemaDDL: []string{
			`
            CREATE FUNCTION identity_change(a integer, OUT x integer, OUT y integer)
                LANGUAGE SQL
                AS $$ SELECT a, a + 1 $$;
		`},
		newSchemaDDL: []string{
			`
            CREATE FUNCTION identity_change(a integer, OUT z integer, OUT y integer)
                LANGUAGE SQL
                AS $$ SELECT a, a + 1 $$;
		`},
	},
	{
		name: "Alter function result type drops the old signature before creating the new one",
		oldSchemaDDL: []string{
			`
            CREATE FUNCTION result_change(a integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                AS $$ SELECT a + 1 $$;
		`},
		newSchemaDDL: []string{
			`
            CREATE FUNCTION result_change(a integer) RETURNS text
                LANGUAGE SQL
                IMMUTABLE
                AS $$ SELECT (a + 1)::text $$;
		`},
	},
	{
		name: "Alter a SQL-standard body (BEGIN ATOMIC) emits CREATE OR REPLACE",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(id INT, foo INT);

            CREATE FUNCTION count_positive_foo() RETURNS BIGINT
                LANGUAGE SQL
            BEGIN ATOMIC
                SELECT count(*) FROM foobar WHERE foo > 0;
            END;
		`},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(id INT, foo INT);

            CREATE FUNCTION count_positive_foo() RETURNS BIGINT
                LANGUAGE SQL
            BEGIN ATOMIC
                SELECT count(*) FROM foobar WHERE id > 0;
            END;
		`},
	},
	{
		name: "Alter a column type a SQL-standard body function reads re-creates the function",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(id INT, foo INT);

            CREATE FUNCTION count_positive_foo() RETURNS BIGINT
                LANGUAGE SQL
            BEGIN ATOMIC
                SELECT count(*) FROM foobar WHERE foo > 0;
            END;
		`},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(id INT, foo BIGINT);

            CREATE FUNCTION count_positive_foo() RETURNS BIGINT
                LANGUAGE SQL
            BEGIN ATOMIC
                SELECT count(*) FROM foobar WHERE foo > 0;
            END;
		`},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAcquiresAccessExclusiveLock,
			diff.MigrationHazardTypeImpactsDatabasePerformance,
		},
	},
	{
		name: "Drop a column a SQL-standard body function reads re-creates the function",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(id INT, foo INT);

            CREATE FUNCTION count_positive_foo() RETURNS BIGINT
                LANGUAGE SQL
            BEGIN ATOMIC
                SELECT count(*) FROM foobar WHERE foo > 0;
            END;
		`},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(id INT);

            CREATE FUNCTION count_positive_foo() RETURNS BIGINT
                LANGUAGE SQL
            BEGIN ATOMIC
                SELECT count(*) FROM foobar WHERE id > 0;
            END;
		`},
		expectedHazardTypes: []diff.MigrationHazardType{diff.MigrationHazardTypeDeletesData},
	},
	{
		name: "Alter non-sql function",
		oldSchemaDDL: []string{
			`
            CREATE FUNCTION non_sql_func(i integer) RETURNS integer AS $$
                    BEGIN
                            RETURN i + 1;
                    END;
            $$ LANGUAGE plpgsql;
		`},
		newSchemaDDL: []string{
			`
            CREATE FUNCTION non_sql_func(i integer) RETURNS integer AS $$
                    BEGIN
                            RETURN i + 5;
                    END;
            $$ LANGUAGE plpgsql;
		`},
		expectedHazardTypes: []diff.MigrationHazardType{diff.MigrationHazardTypeHasUntrackableDependencies},
	},
	{
		name: "Alter sql function to be non-sql function",
		oldSchemaDDL: []string{
			`
            CREATE FUNCTION some_func(i integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURN i + 5;
		`},
		newSchemaDDL: []string{
			`
            CREATE FUNCTION some_func(i integer) RETURNS integer AS $$
                    BEGIN
                            RETURN i + 1;
                    END;
            $$ LANGUAGE plpgsql;
		`},
		expectedHazardTypes: []diff.MigrationHazardType{diff.MigrationHazardTypeHasUntrackableDependencies},
	},
	{
		name: "Alter non-sql function to be sql function (no dependency tracking error)",
		oldSchemaDDL: []string{
			`
            CREATE FUNCTION some_func(i integer) RETURNS integer AS $$
                    BEGIN
                            RETURN i + 1;
                    END;
            $$ LANGUAGE plpgsql;
		`},
		newSchemaDDL: []string{
			`
            CREATE FUNCTION some_func(i integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURN i + 5;
		`},
	},
	{
		name: "Alter a function's dependencies",
		oldSchemaDDL: []string{
			`
            CREATE FUNCTION add(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN a + b;

            CREATE FUNCTION "increment func"(i integer) RETURNS integer AS $$
                    BEGIN
                            RETURN i + 1;
                    END;
            $$ LANGUAGE plpgsql;

            CREATE FUNCTION function_with_dependencies(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN add(a, b) + "increment func"(a);

            -- function with conflicting name to ensure the deps specify param name
            CREATE FUNCTION add(a text, b text) RETURNS text
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN CONCAT(a, b);
			`,
		},
		newSchemaDDL: []string{
			`
        CREATE FUNCTION add(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN a + b;

            CREATE FUNCTION "increment func"(i integer) RETURNS integer AS $$
                    BEGIN
                            RETURN i + 1;
                    END;
            $$ LANGUAGE plpgsql;

            CREATE FUNCTION "decrement func"(i integer) RETURNS integer AS $$
                    BEGIN
                            RETURN i + 1;
                    END;
            $$ LANGUAGE plpgsql;

            CREATE FUNCTION function_with_dependencies(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN add(a, b) + "decrement func"(a);

            -- function with conflicting name to ensure the deps specify param name
            CREATE FUNCTION add(a text, b text) RETURNS text
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN CONCAT(a, b);
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{diff.MigrationHazardTypeHasUntrackableDependencies},
	},
	{
		name: "Alter a dependent function",
		oldSchemaDDL: []string{
			`
            CREATE FUNCTION add(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN a + b;

            CREATE FUNCTION "increment func"(i integer) RETURNS integer AS $$
                    BEGIN
                            RETURN i + 1;
                    END;
            $$ LANGUAGE plpgsql;

            CREATE FUNCTION function_with_dependencies(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN add(a, b) + "increment func"(a);
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE FUNCTION add(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN a + b;

            CREATE FUNCTION "increment func"(i integer) RETURNS int AS $$
                    BEGIN
                            RETURN i + 5;
                    END;
            $$ LANGUAGE plpgsql;

            CREATE FUNCTION function_with_dependencies(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN add(a, b) + "increment func"(a);
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{diff.MigrationHazardTypeHasUntrackableDependencies},
	},
	{
		name: "Alter a function to no longer depend on a function and drop that function",
		oldSchemaDDL: []string{
			`
            CREATE FUNCTION add(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN a + b;

            CREATE FUNCTION "increment func"(i integer) RETURNS integer AS $$
                    BEGIN
                            RETURN i + 1;
                    END;
            $$ LANGUAGE plpgsql;

            CREATE FUNCTION function_with_dependencies(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN add(a, b) + "increment func"(a);
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE FUNCTION add(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN a + b;


            CREATE FUNCTION function_with_dependencies(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN add(a, b);
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{diff.MigrationHazardTypeHasUntrackableDependencies},
	},
	{
		name:         "Create function with name containing double quote",
		oldSchemaDDL: nil,
		newSchemaDDL: []string{
			`
            CREATE FUNCTION "evil""func"(a integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN a + 1;
			`,
		},
	},
	{
		name: "Drop function with name containing double quote",
		oldSchemaDDL: []string{
			`
            CREATE FUNCTION "evil""func"(a integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN a + 1;
			`,
		},
		newSchemaDDL: nil,
	},
	{
		name: "Create function with SQL injection in name",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foo(id integer);
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foo(id integer);
            CREATE FUNCTION "x""; DROP TABLE foo; --"(a integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN a;
			`,
		},
	},
	{
		name: "create a function whose argument is a table row type",
		oldSchemaDDL: []string{
			``,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foo(id INTEGER, name TEXT);
            CREATE FUNCTION takes_foo(f foo) RETURNS INTEGER
                LANGUAGE SQL
                IMMUTABLE
                RETURN f.id;
			`,
		},
		// The table row type is resolved at CREATE time, so the table must come first.
		expectedPlanDDL: []string{
			"CREATE TABLE \"public\".\"foo\" (\n\t\"id\" integer,\n\t\"name\" text COLLATE \"pg_catalog\".\"default\"\n)",
			"ALTER TABLE \"public\".\"foo\" OWNER TO \"postgres\"",
			"CREATE OR REPLACE FUNCTION public.takes_foo(f foo)\n RETURNS integer\n LANGUAGE sql\n IMMUTABLE\nRETURN (f).id\n",
			"ALTER FUNCTION \"public\".\"takes_foo\"(f foo) OWNER TO \"postgres\"",
		},
	},
	{
		name: "create a plpgsql function whose body names a table row type created in the same plan",
		oldSchemaDDL: []string{
			``,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foo(id INTEGER);
            CREATE FUNCTION body_refs_foo() RETURNS INTEGER
                LANGUAGE plpgsql
                AS $$
                DECLARE
                    v foo;
                BEGIN
                    v.id := 1;
                    RETURN v.id;
                END;
                $$;
			`,
		},
		// A routine body's reference to a relation's row type is resolved at CREATE time but is not
		// recorded in pg_depend, so the plan cannot order it. Like pg_dump, the plan is applied with
		// check_function_bodies disabled, so the function may precede the table it names in its body.
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeHasUntrackableDependencies,
		},
		expectedPlanDDL: []string{
			"CREATE OR REPLACE FUNCTION public.body_refs_foo()\n RETURNS integer\n LANGUAGE plpgsql\nAS $function$\n                DECLARE\n                    v foo;\n                BEGIN\n                    v.id := 1;\n                    RETURN v.id;\n                END;\n                $function$\n",
			"ALTER FUNCTION \"public\".\"body_refs_foo\"() OWNER TO \"postgres\"",
			"CREATE TABLE \"public\".\"foo\" (\n\t\"id\" integer\n)",
			"ALTER TABLE \"public\".\"foo\" OWNER TO \"postgres\"",
		},
	},
	{
		name: "create a function whose RETURNS TABLE column is a table row type",
		oldSchemaDDL: []string{
			``,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foo(id INTEGER);
            CREATE FUNCTION returns_foo()
                RETURNS TABLE(whole foo)
                LANGUAGE SQL
                IMMUTABLE
                AS $$ SELECT f FROM foo f $$;
			`,
		},
		expectedPlanDDL: []string{
			"CREATE TABLE \"public\".\"foo\" (\n\t\"id\" integer\n)",
			"ALTER TABLE \"public\".\"foo\" OWNER TO \"postgres\"",
			"CREATE OR REPLACE FUNCTION public.returns_foo()\n RETURNS TABLE(whole foo)\n LANGUAGE sql\n IMMUTABLE\nAS $function$ SELECT f FROM foo f $function$\n",
			"ALTER FUNCTION \"public\".\"returns_foo\"() OWNER TO \"postgres\"",
		},
	},
	{
		name: "create a function whose argument is a view row type",
		oldSchemaDDL: []string{
			``,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foo(id INTEGER);
            CREATE VIEW foo_view AS SELECT id FROM foo;
            CREATE FUNCTION takes_view(v foo_view) RETURNS INTEGER
                LANGUAGE SQL
                IMMUTABLE
                RETURN v.id;
			`,
		},
		// The view's row type is resolved at CREATE time, so the view must come first.
		expectedPlanDDL: []string{
			"CREATE TABLE \"public\".\"foo\" (\n\t\"id\" integer\n)",
			"ALTER TABLE \"public\".\"foo\" OWNER TO \"postgres\"",
			"CREATE VIEW \"public\".\"foo_view\" AS\n SELECT id\n   FROM foo;",
			"ALTER VIEW \"public\".\"foo_view\" OWNER TO \"postgres\"",
			"CREATE OR REPLACE FUNCTION public.takes_view(v foo_view)\n RETURNS integer\n LANGUAGE sql\n IMMUTABLE\nRETURN (v).id\n",
			"ALTER FUNCTION \"public\".\"takes_view\"(v foo_view) OWNER TO \"postgres\"",
		},
	},
	{
		name: "drop a function before the table row type it references",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foo(id INTEGER);
            CREATE FUNCTION takes_foo(f foo) RETURNS INTEGER
                LANGUAGE SQL
                IMMUTABLE
                RETURN f.id;
			`,
		},
		newSchemaDDL: []string{
			``,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeDeletesData,
		},
		expectedPlanDDL: []string{
			"DROP FUNCTION \"public\".\"takes_foo\"(f foo)",
			"DROP TABLE \"public\".\"foo\"",
		},
	},
	{
		name: "domain CHECK calling a routine does not cycle with relation ordering",
		oldSchemaDDL: []string{
			``,
		},
		newSchemaDDL: []string{
			`
            CREATE FUNCTION public.is_nonnegative(n NUMERIC) RETURNS BOOLEAN
                LANGUAGE plpgsql
                IMMUTABLE
                AS $$ BEGIN RETURN n >= 0; END; $$;
            CREATE DOMAIN positive_amount AS NUMERIC
                CONSTRAINT positive_amount_check CHECK (public.is_nonnegative(VALUE));
            CREATE TABLE foo(amount positive_amount);
			`,
		},
		// The routine must precede the domain (the CHECK calls it) and the domain must precede the
		// table (the column is typed with it). The routine's signature references no relation, so
		// ordering routines after their row-type dependencies must not add a table edge that would
		// close the cycle routine -> table -> domain -> routine. Applying the plan is the assertion:
		// a closing edge would order the table first and fail. This is glue between #306 and the
		// relation row-type ordering branch, so it lives here rather than in either PR.
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeHasUntrackableDependencies,
		},
	},
}

func TestFunctionTestCases(t *testing.T) {
	runTestCases(t, functionAcceptanceTestCases)
}
