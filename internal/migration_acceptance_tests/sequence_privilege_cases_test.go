package migration_acceptance_tests

import (
	"testing"

	"github.com/stripe/pg-schema-diff/pkg/diff"
)

var sequencePrivilegeAcceptanceTests = []acceptanceTestCase{
	{
		// A new standalone sequence gets the schema's default sequence grants at CREATE; the
		// target revokes them, so the plan has to revoke them after the create.
		name:  "revoke from a new sequence the privileges the schema's default privileges grant",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1
                    GRANT USAGE, SELECT ON SEQUENCES TO app_user;
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1
                    GRANT USAGE, SELECT ON SEQUENCES TO app_user;
                CREATE SEQUENCE schema_1.foo
                    AS BIGINT
                    INCREMENT BY 1
                    MINVALUE 1 MAXVALUE 100
                    START WITH 1 CACHE 1 NO CYCLE
                    OWNED BY NONE;
                REVOKE USAGE, SELECT ON SEQUENCE schema_1.foo FROM app_user;
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeHasUntrackableDependencies,
		},
	},
	{
		name:  "grant and revoke privileges on an existing sequence",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                CREATE SEQUENCE schema_1.foo
                    AS BIGINT
                    INCREMENT BY 1
                    MINVALUE 1 MAXVALUE 100
                    START WITH 1 CACHE 1 NO CYCLE
                    OWNED BY NONE;
                GRANT USAGE ON SEQUENCE schema_1.foo TO app_user;
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                CREATE SEQUENCE schema_1.foo
                    AS BIGINT
                    INCREMENT BY 1
                    MINVALUE 1 MAXVALUE 100
                    START WITH 1 CACHE 1 NO CYCLE
                    OWNED BY NONE;
                REVOKE USAGE ON SEQUENCE schema_1.foo FROM app_user;
                GRANT SELECT ON SEQUENCE schema_1.foo TO app_user;
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		// The identity column creates its sequence together with the table; the grant must follow.
		name:  "grant on a new identity column's sequence",
		roles: []string{"app_user"},
		newSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                CREATE TABLE schema_1.foo(
                    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY
                );
                GRANT USAGE, SELECT ON SEQUENCE schema_1.foo_id_seq TO app_user;
			`,
		},
	},
	{
		// A serial column models its sequence as a separate object owned by the column; the grant
		// still has to be planned after the sequence exists.
		name:  "grant on a new serial column's sequence",
		roles: []string{"app_user"},
		newSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                CREATE TABLE schema_1.foo(
                    id BIGSERIAL PRIMARY KEY
                );
                GRANT USAGE, SELECT ON SEQUENCE schema_1.foo_id_seq TO app_user;
			`,
		},
	},
	{
		name:  "revoke from a new identity column's sequence the privileges the schema's default privileges grant",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1
                    GRANT USAGE, SELECT ON SEQUENCES TO app_user;
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                ALTER DEFAULT PRIVILEGES IN SCHEMA schema_1
                    GRANT USAGE, SELECT ON SEQUENCES TO app_user;
                CREATE TABLE schema_1.foo(
                    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY
                );
                REVOKE USAGE, SELECT ON SEQUENCE schema_1.foo_id_seq FROM app_user;
			`,
		},
	},
	{
		name:  "grant privileges on an existing identity column's sequence",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                CREATE TABLE schema_1.foo(
                    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY
                );
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                CREATE TABLE schema_1.foo(
                    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY
                );
                GRANT USAGE, SELECT ON SEQUENCE schema_1.foo_id_seq TO app_user;
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
}

func TestSequencePrivilegeTestCases(t *testing.T) {
	runTestCases(t, sequencePrivilegeAcceptanceTests)
}
