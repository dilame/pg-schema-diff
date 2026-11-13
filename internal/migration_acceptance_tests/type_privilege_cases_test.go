package migration_acceptance_tests

import (
	"testing"

	"github.com/stripe/pg-schema-diff/pkg/diff"
)

var typePrivilegeAcceptanceTestCases = []acceptanceTestCase{
	{
		name:  "no-op type and domain privileges",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`
                CREATE DOMAIN foo AS INT;
                GRANT USAGE ON DOMAIN foo TO app_user;
                CREATE TYPE bar AS ENUM ('a', 'b');
                GRANT USAGE ON TYPE bar TO app_user;
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE DOMAIN foo AS INT;
                GRANT USAGE ON DOMAIN foo TO app_user;
                CREATE TYPE bar AS ENUM ('a', 'b');
                GRANT USAGE ON TYPE bar TO app_user;
			`,
		},
		expectEmptyPlan: true,
	},
	{
		name:  "Grant USAGE on an existing domain",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`CREATE DOMAIN foo AS INT;`,
		},
		newSchemaDDL: []string{
			`
				CREATE DOMAIN foo AS INT;
				GRANT USAGE ON DOMAIN foo TO app_user;
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
		expectedPlanDDL: []string{`GRANT USAGE ON DOMAIN "public"."foo" TO "app_user"`},
	},
	{
		name:  "Revoke USAGE on an existing domain returns to the default",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`
                CREATE DOMAIN foo AS INT;
                GRANT USAGE ON DOMAIN foo TO app_user;
			`,
		},
		newSchemaDDL: []string{
			`CREATE DOMAIN foo AS INT;`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
		expectedPlanDDL: []string{`REVOKE USAGE ON DOMAIN "public"."foo" FROM "app_user"`},
	},
	{
		name:  "Grant USAGE on a new domain",
		roles: []string{"app_user"},
		newSchemaDDL: []string{
			`
                CREATE DOMAIN foo AS INT;
                GRANT USAGE ON DOMAIN foo TO app_user;
			`,
		},
		expectedPlanDDL: []string{
			`CREATE DOMAIN "public"."foo" AS integer`,
			`GRANT USAGE ON DOMAIN "public"."foo" TO "app_user"`,
		},
	},
	{
		name:  "Grant USAGE on an existing enum",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`CREATE TYPE foo AS ENUM ('a', 'b');`,
		},
		newSchemaDDL: []string{
			`
				CREATE TYPE foo AS ENUM ('a', 'b');
				GRANT USAGE ON TYPE foo TO app_user;
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
		expectedPlanDDL: []string{`GRANT USAGE ON TYPE "public"."foo" TO "app_user"`},
	},
	{
		name:  "Revoke USAGE on an existing enum returns to the default",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`
                CREATE TYPE foo AS ENUM ('a', 'b');
                GRANT USAGE ON TYPE foo TO app_user;
			`,
		},
		newSchemaDDL: []string{
			`CREATE TYPE foo AS ENUM ('a', 'b');`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
		expectedPlanDDL: []string{`REVOKE USAGE ON TYPE "public"."foo" FROM "app_user"`},
	},
	{
		name:  "Grant USAGE on an existing composite type",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`CREATE TYPE foo AS (a INT, b TEXT);`,
		},
		newSchemaDDL: []string{
			`
				CREATE TYPE foo AS (a INT, b TEXT);
				GRANT USAGE ON TYPE foo TO app_user;
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
		expectedPlanDDL: []string{`GRANT USAGE ON TYPE "public"."foo" TO "app_user"`},
	},
	{
		name:  "Grant USAGE WITH GRANT OPTION on a domain",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`
                CREATE DOMAIN foo AS INT;
                GRANT USAGE ON DOMAIN foo TO app_user;
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE DOMAIN foo AS INT;
                GRANT USAGE ON DOMAIN foo TO app_user WITH GRANT OPTION;
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
		expectedPlanDDL: []string{`GRANT USAGE ON DOMAIN "public"."foo" TO "app_user" WITH GRANT OPTION`},
	},
	{
		name:  "Remove GRANT OPTION from a domain privilege",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`
				CREATE DOMAIN foo AS INT;
				GRANT USAGE ON DOMAIN foo TO app_user WITH GRANT OPTION;
			`,
		},
		newSchemaDDL: []string{
			`
				CREATE DOMAIN foo AS INT;
				GRANT USAGE ON DOMAIN foo TO app_user;
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
		expectedPlanDDL: []string{`REVOKE GRANT OPTION FOR USAGE ON DOMAIN "public"."foo" FROM "app_user"`},
	},
	{
		name:  "Recreating a domain keeps its privileges",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`
                CREATE DOMAIN foo AS INT;
                GRANT USAGE ON DOMAIN foo TO app_user;
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE DOMAIN foo AS BIGINT;
                GRANT USAGE ON DOMAIN foo TO app_user;
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeHasUntrackableDependencies,
		},
		expectedPlanDDL: []string{
			`DROP DOMAIN "public"."foo"`,
			`CREATE DOMAIN "public"."foo" AS bigint`,
			`GRANT USAGE ON DOMAIN "public"."foo" TO "app_user"`,
		},
	},
}

func TestTypePrivilegeCases(t *testing.T) {
	runTestCases(t, typePrivilegeAcceptanceTestCases)
}
