package migration_acceptance_tests

import (
	"testing"

	"github.com/stripe/pg-schema-diff/pkg/diff"
)

// PUBLIC holds USAGE on every type by default. A target that declares
// `REVOKE USAGE ... FROM PUBLIC` is a different schema from one that keeps the
// default, and the plan must carry the REVOKE just as it does for routines.
var typePrivilegePublicUsageTestCases = []acceptanceTestCase{
	{
		name:  "Revoke default PUBLIC USAGE on an existing composite type",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`CREATE TYPE foo AS (a INT);`,
		},
		newSchemaDDL: []string{
			`
			CREATE TYPE foo AS (a INT);
			REVOKE USAGE ON TYPE foo FROM PUBLIC;
			GRANT USAGE ON TYPE foo TO app_user;
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
		expectedPlanDDL: []string{
			`GRANT USAGE ON TYPE "public"."foo" TO "app_user"`,
			`REVOKE USAGE ON TYPE "public"."foo" FROM PUBLIC`,
		},
	},
	{
		name:  "Revoke default PUBLIC USAGE on an existing enum",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`CREATE TYPE foo AS ENUM ('a', 'b');`,
		},
		newSchemaDDL: []string{
			`
			CREATE TYPE foo AS ENUM ('a', 'b');
			REVOKE USAGE ON TYPE foo FROM PUBLIC;
			GRANT USAGE ON TYPE foo TO app_user;
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
		expectedPlanDDL: []string{
			`GRANT USAGE ON TYPE "public"."foo" TO "app_user"`,
			`REVOKE USAGE ON TYPE "public"."foo" FROM PUBLIC`,
		},
	},
	{
		name:  "Revoke default PUBLIC USAGE on an existing domain",
		roles: []string{"app_user"},
		oldSchemaDDL: []string{
			`CREATE DOMAIN foo AS INT;`,
		},
		newSchemaDDL: []string{
			`
			CREATE DOMAIN foo AS INT;
			REVOKE USAGE ON DOMAIN foo FROM PUBLIC;
			GRANT USAGE ON DOMAIN foo TO app_user;
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
		expectedPlanDDL: []string{
			`GRANT USAGE ON DOMAIN "public"."foo" TO "app_user"`,
			`REVOKE USAGE ON DOMAIN "public"."foo" FROM PUBLIC`,
		},
	},
	{
		name:  "New type revokes PUBLIC USAGE at creation",
		roles: []string{"app_user"},
		newSchemaDDL: []string{
			`
			CREATE TYPE foo AS ENUM ('a', 'b');
			REVOKE USAGE ON TYPE foo FROM PUBLIC;
			GRANT USAGE ON TYPE foo TO app_user;
			`,
		},
		expectedPlanDDL: []string{
			`CREATE TYPE "public"."foo" AS ENUM ('a', 'b')`,
			`ALTER TYPE "public"."foo" OWNER TO "postgres"`,
			`REVOKE USAGE ON TYPE "public"."foo" FROM PUBLIC`,
			`GRANT USAGE ON TYPE "public"."foo" TO "app_user"`,
		},
	},
	{
		name: "Restore default PUBLIC USAGE on an existing composite type",
		oldSchemaDDL: []string{
			`
			CREATE TYPE foo AS (a INT);
			REVOKE USAGE ON TYPE foo FROM PUBLIC;
			`,
		},
		newSchemaDDL: []string{
			`CREATE TYPE foo AS (a INT);`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
		expectedPlanDDL: []string{
			`GRANT USAGE ON TYPE "public"."foo" TO PUBLIC`,
		},
	},
}

func TestTypePrivilegePublicUsage(t *testing.T) {
	runTestCases(t, typePrivilegePublicUsageTestCases)
}
