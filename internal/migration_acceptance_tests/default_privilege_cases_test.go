package migration_acceptance_tests

import (
	"testing"

	"github.com/stripe/pg-schema-diff/pkg/diff"
)

var defaultPrivilegeAcceptanceTestCases = []acceptanceTestCase{
	{
		name:  "no-op",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1 GRANT SELECT ON TABLES TO app_user;
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1 GRANT SELECT ON TABLES TO app_user;
			`,
		},
		expectEmptyPlan: true,
	},
	{
		name:  "grant default privileges on a new schema",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			``,
		},
		newSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1
                    GRANT SELECT, INSERT ON TABLES TO app_user;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1
                    GRANT USAGE ON SEQUENCES TO app_user;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1
                    GRANT EXECUTE ON FUNCTIONS TO app_user;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1
                    GRANT USAGE ON TYPES TO app_user;
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name:  "grant default privileges on an existing schema",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                CREATE TABLE schema_1.foo(id INT);
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                CREATE TABLE schema_1.foo(id INT);
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1 GRANT SELECT ON TABLES TO app_user;
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name:  "revoke default privileges",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1
                    GRANT SELECT, INSERT ON TABLES TO app_user;
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name:  "change the granted default privileges",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1
                    GRANT SELECT, INSERT ON TABLES TO app_user;
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1
                    GRANT SELECT, UPDATE ON TABLES TO app_user;
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name:  "add and remove the grant option",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1
                    GRANT SELECT ON TABLES TO app_user;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1
                    GRANT INSERT ON TABLES TO app_user WITH GRANT OPTION;
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1
                    GRANT SELECT ON TABLES TO app_user WITH GRANT OPTION;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1
                    GRANT INSERT ON TABLES TO app_user;
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name:  "grant default privileges to PUBLIC",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1 GRANT SELECT ON TABLES TO PUBLIC;
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name:  "drop the schema holding the default privileges",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1 GRANT SELECT ON TABLES TO app_user;
			`,
		},
		newSchemaDDL: []string{
			``,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		// A function created in a schema whose default privileges grant EXECUTE to a role comes
		// out of CREATE with that grant. The target revokes it, so the plan has to revoke it after
		// the create; without that, the migrated database keeps a grant the target does not hold.
		name:  "revoke from a new function the execute the schema's default privileges grant",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1
                    GRANT EXECUTE ON FUNCTIONS TO app_user;
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1
                    GRANT EXECUTE ON FUNCTIONS TO app_user;
                CREATE FUNCTION schema_1.private_value() RETURNS INT
                    LANGUAGE sql IMMUTABLE
                    RETURN 1;
                REVOKE EXECUTE ON FUNCTION schema_1.private_value() FROM app_user;
			`,
		},
		expectedPlanDDL: []string{
			"CREATE OR REPLACE FUNCTION schema_1.private_value()\n RETURNS integer\n LANGUAGE sql\n IMMUTABLE\nRETURN 1\n",
			"ALTER FUNCTION \"schema_1\".\"private_value\"() OWNER TO \"postgres\"",
			"REVOKE EXECUTE ON FUNCTION \"schema_1\".\"private_value\"() FROM \"app_user\"",
		},
	},
	{
		// CREATE OR REPLACE keeps the function's ACL, so a replaced function does not pick up the
		// default privilege the target added; the plan has to grant it explicitly.
		name:  "grant a default privilege to a function that is replaced",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                CREATE FUNCTION schema_1.value() RETURNS INT
                    LANGUAGE sql IMMUTABLE
                    RETURN 1;
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1
                    GRANT EXECUTE ON FUNCTIONS TO app_user;
                CREATE FUNCTION schema_1.value() RETURNS INT
                    LANGUAGE sql IMMUTABLE
                    RETURN 2;
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name:  "revoke from a new procedure the execute the schema's default privileges grant",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1
                    GRANT EXECUTE ON FUNCTIONS TO app_user;
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1
                    GRANT EXECUTE ON FUNCTIONS TO app_user;
                CREATE PROCEDURE schema_1.do_nothing()
                    LANGUAGE sql
                    AS $$ SELECT 1 $$;
                REVOKE EXECUTE ON PROCEDURE schema_1.do_nothing() FROM app_user;
			`,
		},
		// A procedure's dependencies are not tracked by Postgres, so its create always carries
		// this hazard.
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeHasUntrackableDependencies,
		},
	},
	{
		name:  "revoke from new relations the select the schema's default privileges grant",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1
                    GRANT SELECT ON TABLES TO app_user;
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1
                    GRANT SELECT ON TABLES TO app_user;
                CREATE TABLE schema_1.foo(id INT);
                REVOKE SELECT ON schema_1.foo FROM app_user;
                CREATE VIEW schema_1.foo_view AS SELECT id FROM schema_1.foo;
                REVOKE SELECT ON schema_1.foo_view FROM app_user;
                CREATE MATERIALIZED VIEW schema_1.foo_mv AS SELECT id FROM schema_1.foo;
                REVOKE SELECT ON schema_1.foo_mv FROM app_user;
			`,
		},
	},
	{
		name:  "revoke from new types the usage the schema's default privileges grant",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1
                    GRANT USAGE ON TYPES TO app_user;
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1
                    GRANT USAGE ON TYPES TO app_user;
                CREATE TYPE schema_1.mood AS ENUM ('sad', 'ok');
                REVOKE USAGE ON TYPE schema_1.mood FROM app_user;
                CREATE DOMAIN schema_1.positive AS INT CHECK (VALUE > 0);
                REVOKE USAGE ON DOMAIN schema_1.positive FROM app_user;
                CREATE TYPE schema_1.pair AS (a INT, b INT);
                REVOKE USAGE ON TYPE schema_1.pair FROM app_user;
			`,
		},
	},
	{
		// A default grant carrying the grant option and a target grant without it cannot be
		// resolved by GRANT alone: the option survives a plain GRANT, so the plan revokes the
		// default grant and grants the target one again.
		name:  "a new table's default grant option is revoked before the target grant",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1
                    GRANT SELECT ON TABLES TO app_user WITH GRANT OPTION;
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1
                    GRANT SELECT ON TABLES TO app_user WITH GRANT OPTION;
                CREATE TABLE schema_1.foo(id INT);
                REVOKE GRANT OPTION FOR SELECT ON schema_1.foo FROM app_user;
			`,
		},
	},
	{
		// A database-wide rule is not attached to any schema, so it is not extracted and never
		// shows up in a plan — not even as the revoke that would otherwise pair with the
		// schema-scoped grant below.
		name:  "database-wide default privileges are out of scope",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO app_user;
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO app_user;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1 GRANT SELECT ON TABLES TO app_user;
			`,
		},
		expectedPlanDDL: []string{
			`ALTER DEFAULT PRIVILEGES FOR ROLE "postgres" IN SCHEMA "schema_1" GRANT SELECT ON TABLES TO "app_user"`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		// ALTER DEFAULT PRIVILEGES FOR ROLE other_role only changes what other_role's CREATE
		// leaves behind. The plan runs as postgres, so the new table does not receive the default
		// grant and the target's explicit grant has to be planned.
		name:  "a default privilege for another target role does not apply to a new table",
		roles: []string{"app_user", "other_role"},
		oldSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES FOR ROLE other_role IN SCHEMA schema_1
                    GRANT SELECT ON TABLES TO app_user;
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES FOR ROLE other_role IN SCHEMA schema_1
                    GRANT SELECT ON TABLES TO app_user;
                CREATE TABLE schema_1.foo(id INT);
                GRANT SELECT ON schema_1.foo TO app_user;
			`,
		},
		expectedPlanDDL: []string{
			"CREATE TABLE \"schema_1\".\"foo\" (\n\t\"id\" integer\n)",
			"ALTER TABLE \"schema_1\".\"foo\" OWNER TO \"postgres\"",
			"GRANT SELECT ON \"schema_1\".\"foo\" TO \"app_user\"",
		},
	},
	{
		// The sequence path follows the same rule: the other_role's SEQUENCES rule does not reach
		// the sequence postgres creates, so the target's explicit grant is planned.
		name:  "a default privilege for another target role does not apply to a new sequence",
		roles: []string{"app_user", "other_role"},
		oldSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES FOR ROLE other_role IN SCHEMA schema_1
                    GRANT SELECT ON SEQUENCES TO app_user;
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES FOR ROLE other_role IN SCHEMA schema_1
                    GRANT SELECT ON SEQUENCES TO app_user;
                CREATE SEQUENCE schema_1.foo
                    AS BIGINT
                    INCREMENT BY 1
                    MINVALUE 1 MAXVALUE 100
                    START WITH 1 CACHE 1 NO CYCLE
                    OWNED BY NONE;
                GRANT SELECT ON SEQUENCE schema_1.foo TO app_user;
			`,
		},
		expectedPlanDDL: []string{
			"CREATE SEQUENCE \"schema_1\".\"foo\"\n\tAS bigint\n\tINCREMENT BY 1\n\tMINVALUE 1 MAXVALUE 100\n\tSTART WITH 1 CACHE 1 NO CYCLE\n",
			"ALTER SEQUENCE \"schema_1\".\"foo\" OWNER TO \"postgres\"",
			"GRANT SELECT ON \"schema_1\".\"foo\" TO \"app_user\"",
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeHasUntrackableDependencies,
		},
	},
}

func TestDefaultPrivilegeTestCases(t *testing.T) {
	runTestCases(t, defaultPrivilegeAcceptanceTestCases)
}
