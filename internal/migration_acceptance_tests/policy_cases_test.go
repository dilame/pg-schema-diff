package migration_acceptance_tests

import (
	"testing"

	"github.com/stripe/pg-schema-diff/pkg/diff"
)

var policyAcceptanceTestCases = []acceptanceTestCase{
	{
		name: "no-op",
		roles: []string{
			"role_1",
			"role_2",
		},
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar 
                    AS PERMISSIVE
                    FOR ALL 
                    TO role_1, role_2 
                    USING (true)
                    WITH CHECK (true);
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar 
                    AS PERMISSIVE
                    FOR ALL 
                    TO role_1, role_2 
                    USING (true)
                    WITH CHECK (true);
			`,
		},
		expectEmptyPlan: true,
	},
	{
		name: "Add permissive ALL policy target on non-public schema",
		roles: []string{
			"role_1",
			"role_2",
		},
		oldSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                CREATE TABLE schema_1.foobar();
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                CREATE TABLE schema_1.foobar();
                CREATE POLICY foobar_policy ON schema_1.foobar 
                    AS PERMISSIVE
                    FOR ALL 
                    TO role_1, role_2
                    USING (true)
                    WITH CHECK (true);
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name: "Create SELECT policy",
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar();
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar 
                    AS RESTRICTIVE
                    FOR SELECT 
                    TO PUBLIC
                    USING (true);
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name: "Create INSERT policy",
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar();
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar 
                    AS RESTRICTIVE
                    FOR INSERT 
                    TO PUBLIC
                    WITH CHECK (true);
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name: "Create UPDATE policy",
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar();
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar 
                    AS RESTRICTIVE
                    FOR UPDATE 
                    TO PUBLIC
                    WITH CHECK (true);
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name: "Create DELETE policy",
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar();
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar 
                    AS RESTRICTIVE
                    FOR SELECT 
                    TO PUBLIC
                    USING (true);
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name: "Add policy on new table",
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar 
                    AS RESTRICTIVE
                    FOR SELECT 
                    TO PUBLIC
                    USING (true);
			`,
		},
	},
	{
		name: "Add policy then enable RLS",
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar();
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                ALTER TABLE foobar ENABLE ROW LEVEL SECURITY;
                ALTER TABLE foobar FORCE ROW LEVEL SECURITY;
                CREATE POLICY foobar_policy ON foobar 
                    AS RESTRICTIVE
                    FOR SELECT 
                    TO PUBLIC
                    USING (true);
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
		expectedPlanDDL: []string{
			// Ensure that the policy is created before enabling RLS.
			"CREATE POLICY \"foobar_policy\" ON \"public\".\"foobar\"\n\tAS RESTRICTIVE\n\tFOR SELECT\n\tTO PUBLIC\n\tUSING (true)",
			"ALTER TABLE \"public\".\"foobar\" ENABLE ROW LEVEL SECURITY",
			"ALTER TABLE \"public\".\"foobar\" FORCE ROW LEVEL SECURITY",
		},
	},
	{
		name: "Drop non-public schema policy",
		oldSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                CREATE TABLE schema_1.foobar();
                CREATE POLICY foobar_policy ON schema_1.foobar 
                    AS RESTRICTIVE
                    FOR SELECT 
                    TO PUBLIC
                    USING (true);
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                CREATE TABLE schema_1.foobar();
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name: "Drop policy and table",
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar 
                    AS RESTRICTIVE
                    FOR SELECT 
                    TO PUBLIC
                    USING (true);
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeDeletesData,
		},
	},
	{
		name: "Disable RLS then drop policy",
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar 
                    AS RESTRICTIVE
                    FOR SELECT 
                    TO PUBLIC
                    USING (true);
                ALTER TABLE foobar ENABLE ROW LEVEL SECURITY;
                ALTER TABLE foobar FORCE ROW LEVEL SECURITY;
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar();
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
		expectedPlanDDL: []string{
			"ALTER TABLE \"public\".\"foobar\" DISABLE ROW LEVEL SECURITY",
			"ALTER TABLE \"public\".\"foobar\" NO FORCE ROW LEVEL SECURITY",
			"DROP POLICY \"foobar_policy\" ON \"public\".\"foobar\"",
		},
	},
	{
		name: "Drop policy and columns",
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar(
                        category TEXT,
                        val TEXT
                );
                CREATE POLICY foobar_policy ON foobar 
                    AS RESTRICTIVE
                    FOR SELECT 
                    TO PUBLIC
                    USING (category = 'category' AND val = 'value');
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar();
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeDeletesData,
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name: "Restrictive to permissive policy",
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar 
                    AS PERMISSIVE 
                    FOR SELECT 
                    TO PUBLIC
                    USING (true);
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar 
                    AS RESTRICTIVE 
                    FOR SELECT 
                    TO PUBLIC
                    USING (true);
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name: "Alter policy target",
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar 
                    AS PERMISSIVE 
                    FOR SELECT 
                    TO PUBLIC
                    USING (true);
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar 
                    AS PERMISSIVE 
                    FOR DELETE 
                    TO PUBLIC
                    USING (true);
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name:  "Alter policy applies to",
		roles: []string{"role_1", "role_2"},
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar 
                    AS PERMISSIVE 
                    FOR SELECT 
                    TO PUBLIC
                    USING (true);
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar 
                    AS PERMISSIVE 
                    FOR SELECT 
                    TO role_1, role_2
                    USING (true);
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name: "Alter policy using",
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar 
                    AS PERMISSIVE 
                    FOR SELECT 
                    TO PUBLIC
                    USING (true);
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar 
                    AS PERMISSIVE 
                    FOR SELECT 
                    TO PUBLIC
                    USING (false);
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name: "Alter policy check",
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar 
                    AS PERMISSIVE 
                    FOR INSERT 
                    TO PUBLIC
                    WITH CHECK (true);
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar 
                    AS PERMISSIVE 
                    FOR INSERT 
                    TO PUBLIC
                    WITH CHECK (false);
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name: "Remove using check for ALL policy",
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar 
                    AS PERMISSIVE 
                    FOR ALL 
                    TO PUBLIC
                    USING (true)
                    WITH CHECK (true);
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar 
                    AS PERMISSIVE 
                    FOR ALL 
                    TO PUBLIC
                    WITH CHECK (true);
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name: "Remove check for ALL policy",
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar 
                    AS PERMISSIVE 
                    FOR ALL 
                    TO PUBLIC
                    USING (true)
                    WITH CHECK (true);
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar 
                    AS PERMISSIVE 
                    FOR ALL 
                    TO PUBLIC
                    USING (true);
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name: "Alter all alterable attributes on non-public schema",
		roles: []string{
			"role_1",
			"role_2",
		},
		oldSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                CREATE TABLE schema_1.foobar();
                CREATE POLICY foobar_policy ON schema_1.foobar 
                    AS PERMISSIVE 
                    FOR ALL 
                    TO role_1, role_2
                    USING (true)
                    WITH CHECK (true);
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE SCHEMA schema_1;
                CREATE TABLE schema_1.foobar();
                CREATE POLICY foobar_policy ON schema_1.foobar 
                    AS PERMISSIVE 
                    FOR ALL 
                    TO PUBLIC
                    USING (false)
                    WITH CHECK (false);
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name: "Alter policy that references deleted columns and new columns (non-public schema)",
		oldSchemaDDL: []string{
			`
            CREATE SCHEMA schema_1;
            CREATE TABLE schema_1.foobar(
                category TEXT,
                val TEXT
            );
            CREATE POLICY foobar_policy ON schema_1.foobar 
                AS PERMISSIVE 
                FOR ALL 
                TO PUBLIC
                USING (val = 'value' AND category = 'category')
                WITH CHECK (val = 'value');
		`,
		},
		newSchemaDDL: []string{
			`
            CREATE SCHEMA schema_1;
            CREATE TABLE schema_1.foobar(
                category TEXT NOT NULL,
                new_val TEXT
            );
            CREATE POLICY foobar_policy ON schema_1.foobar 
                AS PERMISSIVE 
                FOR ALL 
                TO PUBLIC
                USING (new_val = 'value' AND category = 'category')
                WITH CHECK (new_val = 'value');
		`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
			diff.MigrationHazardTypeDeletesData,
		},
	},
	{
		name: "Re-create policy that references deleted columns and new columns (non-public schema)",
		oldSchemaDDL: []string{
			`
            CREATE SCHEMA schema_1;
            CREATE TABLE schema_1.foobar(
                category TEXT,
                val TEXT
            );
            CREATE POLICY foobar_policy ON schema_1.foobar 
                AS PERMISSIVE 
                FOR ALL 
                TO PUBLIC
                USING (val = 'value' AND category = 'category')
                WITH CHECK (val = 'value');
		`,
		},
		newSchemaDDL: []string{
			`
            CREATE SCHEMA schema_1;
            CREATE TABLE schema_1.foobar(
                category TEXT NOT NULL,
                new_val TEXT
            );
            CREATE POLICY foobar_policy ON schema_1.foobar 
                AS RESTRICTIVE -- force-recreate the policy 
                FOR ALL 
                TO PUBLIC
                USING (new_val = 'value' AND category = 'category')
                WITH CHECK (new_val = 'value');
		`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
			diff.MigrationHazardTypeDeletesData,
		},
	},
	{
		name: "Alter policy (table is re-created)",
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar(
                    category TEXT
                ) partition by list (category);
                CREATE POLICY foobar_policy ON foobar 
                    AS PERMISSIVE 
                    FOR INSERT 
                    TO PUBLIC
                    WITH CHECK (true);
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar(
                    category TEXT,
                    some_new_column TEXT
                ); -- Re-create by removing partitioning
                CREATE POLICY foobar_policy ON foobar 
                    AS PERMISSIVE 
                    FOR INSERT 
                    TO PUBLIC
                    WITH CHECK (category = 'category' AND some_new_column = 'value');
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeDeletesData,
		},
	},
	{
		name: "Policy on new partition (not implemented)",
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar(
                    category TEXT
                ) partition by list (category);
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar(
                    category TEXT
                ) partition by list (category);
                CREATE TABLE foobar_1 PARTITION OF foobar FOR VALUES IN ('category');
                CREATE POLICY foobar_1_policy ON foobar_1 
                    AS PERMISSIVE 
                    FOR INSERT 
                    TO PUBLIC
                    WITH CHECK (true);
			`,
		},

		expectedPlanErrorIs: diff.ErrNotImplemented,
	},
	{
		name: "Add policy on existing partition (not implemented)",
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar(
                    category TEXT
                ) partition by list (category);
                CREATE TABLE foobar_1 PARTITION OF foobar FOR VALUES IN ('category');
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar(
                    category TEXT
                ) partition by list (category);
                CREATE TABLE foobar_1 PARTITION OF foobar FOR VALUES IN ('category');
                CREATE POLICY foobar_1_policy ON foobar_1 
                    AS PERMISSIVE 
                    FOR INSERT 
                    TO PUBLIC
                    WITH CHECK (true);
			`,
		},

		expectedPlanErrorIs: diff.ErrNotImplemented,
	},
	{
		name: "Create policy with role containing double quote",
		roles: []string{
			`"evil""role"`,
		},
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar();
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar
                    AS PERMISSIVE
                    FOR ALL
                    TO "evil""role"
                    USING (true)
                    WITH CHECK (true);
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name: "Alter policy with role containing SQL injection payload",
		roles: []string{
			`"x""; DROP TABLE foobar; --"`,
		},
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar
                    AS PERMISSIVE
                    FOR ALL
                    TO PUBLIC
                    USING (true)
                    WITH CHECK (true);
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar
                    AS PERMISSIVE
                    FOR ALL
                    TO "x""; DROP TABLE foobar; --"
                    USING (true)
                    WITH CHECK (true);
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name: "Create policy with PUBLIC keyword unquoted",
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar();
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar();
                CREATE POLICY foobar_policy ON foobar
                    AS PERMISSIVE
                    FOR ALL
                    TO PUBLIC
                    USING (true)
                    WITH CHECK (true);
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name: "Create policy whose expression calls a function created in the same plan",
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar(id uuid);
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar(id uuid);
                CREATE FUNCTION current_user_id() RETURNS uuid
                    LANGUAGE sql STABLE
                    AS $$ SELECT '00000000-0000-0000-0000-000000000000'::uuid $$;
                CREATE POLICY foobar_policy ON foobar
                    AS PERMISSIVE
                    FOR SELECT
                    TO PUBLIC
                    USING (id = current_user_id());
			`,
		},
		// PostgreSQL resolves the function the USING expression calls at CREATE POLICY time, so the
		// function must come first. Applying the plan is the assertion: the wrong order would fail
		// with `function current_user_id() does not exist`.
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name: "Create policy reading a table created in the same plan",
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar(id INT, grant_id INT);
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar(id INT, grant_id INT);
                CREATE TABLE foobar_grant(id INT, grantee UUID);
                CREATE POLICY foobar_policy ON foobar
                    AS PERMISSIVE
                    FOR SELECT
                    TO PUBLIC
                    USING (EXISTS (
                        SELECT 1 FROM foobar_grant g WHERE g.id = foobar.grant_id
                    ));
			`,
		},
		// PostgreSQL resolves the relation the USING expression reads at CREATE POLICY time, and the
		// policy is emitted with its table, so the table must come after that relation.
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
	{
		name:         "Create a policy calling a function whose SQL-standard body reads the policy's own table",
		oldSchemaDDL: nil,
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar(id INT);
                CREATE FUNCTION row_count() RETURNS bigint
                    LANGUAGE SQL
                BEGIN ATOMIC
                    SELECT count(*) FROM foobar WHERE id > 0;
                END;
                CREATE POLICY foobar_policy ON foobar
                    AS PERMISSIVE
                    FOR SELECT
                    TO PUBLIC
                    USING (row_count() > 0);
		`},
		// The real order is table → function → policy: the function's body reads the table, and the
		// policy calls the function. Keeping the policy inside the table's vertex would make it
		// table → function and function → table, a cycle that fails plan generation.
	},
	{
		name:  "re-create a policy with its roles, expressions and comment around a function whose argument type changes",
		roles: []string{"reader"},
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(id INT, owner_id INT);
            ALTER TABLE foobar ENABLE ROW LEVEL SECURITY;

            CREATE FUNCTION is_owner(p INT) RETURNS BOOLEAN
                LANGUAGE sql STABLE
                RETURN p > 0;

            CREATE POLICY foobar_owner ON foobar
                AS RESTRICTIVE
                FOR UPDATE
                TO reader
                USING (is_owner(owner_id))
                WITH CHECK (is_owner(id));
            COMMENT ON POLICY foobar_owner ON foobar IS 'owner';
		`},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(id INT, owner_id INT);
            ALTER TABLE foobar ENABLE ROW LEVEL SECURITY;

            CREATE FUNCTION is_owner(p BIGINT) RETURNS BOOLEAN
                LANGUAGE sql STABLE
                RETURN p > 0;

            CREATE POLICY foobar_owner ON foobar
                AS RESTRICTIVE
                FOR UPDATE
                TO reader
                USING (is_owner(owner_id))
                WITH CHECK (is_owner(id));
            COMMENT ON POLICY foobar_owner ON foobar IS 'owner';
		`},
		// The old signature is dropped, so the policy that calls it is dropped first and created
		// again after the new signature exists, with its command, roles, expressions and comment.
		// Between the two the restrictive policy is absent, which the hazards say.
		expectedHazardTypes: []diff.MigrationHazardType{diff.MigrationHazardTypeAuthzUpdate},
	},
	{
		name: "Alter policy to stop reading a table the plan drops",
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar(id INT, grant_id INT);
                CREATE TABLE foobar_grant(id INT, grantee UUID);
                CREATE VIEW foobar_view AS SELECT id FROM foobar;
                CREATE POLICY foobar_policy ON foobar
                    AS PERMISSIVE
                    FOR SELECT
                    TO PUBLIC
                    USING (EXISTS (
                        SELECT 1 FROM foobar_grant g WHERE g.id = foobar.grant_id
                    ));
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar(id INT, grant_id INT);
                CREATE TABLE foobar_access(foobar_id INT, grantee UUID);
                CREATE POLICY foobar_policy ON foobar
                    AS PERMISSIVE
                    FOR SELECT
                    TO PUBLIC
                    USING (EXISTS (
                        SELECT 1 FROM foobar_access a WHERE a.foobar_id = foobar.id
                    ));
			`,
		},
		// PostgreSQL refuses DROP TABLE foobar_grant while the policy's expression still reads it,
		// so the ALTER POLICY has to run before the drop (and after foobar_access exists). The
		// dropped view holds the table's own vertex back behind a deletion, so only an explicit
		// dependency keeps it ahead of the drop of foobar_grant.
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
			diff.MigrationHazardTypeDeletesData,
		},
	},
	{
		name: "Alter policy to stop reading a dropped table whose own policy reads the altered table",
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar(id INT, grant_id INT);
                CREATE TABLE foobar_grant(id INT, foobar_id INT);
                CREATE POLICY foobar_policy ON foobar
                    AS PERMISSIVE
                    FOR SELECT
                    TO PUBLIC
                    USING (EXISTS (
                        SELECT 1 FROM foobar_grant g WHERE g.id = foobar.grant_id
                    ));
                CREATE POLICY foobar_grant_policy ON foobar_grant
                    AS PERMISSIVE
                    FOR SELECT
                    TO PUBLIC
                    USING (EXISTS (
                        SELECT 1 FROM foobar f WHERE f.id = foobar_grant.foobar_id
                    ));
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar(id INT, grant_id INT);
                CREATE POLICY foobar_policy ON foobar
                    AS PERMISSIVE
                    FOR SELECT
                    TO PUBLIC
                    USING (grant_id IS NOT NULL);
			`,
		},
		// The two policies read each other's tables. foobar's policy has to change before
		// foobar_grant is dropped; foobar_grant's policy goes with its table and needs foobar to
		// exist, which it does throughout. Ordering the drop before foobar's (no-op) delete would
		// put it before foobar's alter too, a cycle with the first requirement.
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
			diff.MigrationHazardTypeDeletesData,
		},
	},
	{
		name: "Drop policy of an existing table together with the table it reads",
		oldSchemaDDL: []string{
			`
                CREATE TABLE foobar(id INT, grant_id INT);
                CREATE VIEW foobar_grant AS SELECT 1 AS id;
                CREATE VIEW foobar_view AS SELECT id FROM foobar;
                CREATE POLICY foobar_policy ON foobar
                    AS PERMISSIVE
                    FOR SELECT
                    TO PUBLIC
                    USING (EXISTS (
                        SELECT 1 FROM foobar_grant g WHERE g.id = foobar.grant_id
                    ));
			`,
		},
		newSchemaDDL: []string{
			`
                CREATE TABLE foobar(id INT, grant_id INT);
			`,
		},
		// The relation is a view here, so the order holds for every relation kind a policy reads.
		// The dropped foobar_view holds the table's vertex, which drops the policy, back behind a
		// deletion, as in the case above.
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAuthzUpdate,
		},
	},
}

func TestPolicyCases(t *testing.T) {
	runTestCases(t, policyAcceptanceTestCases)
}
