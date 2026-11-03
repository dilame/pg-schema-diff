package migration_acceptance_tests

import (
	"testing"

	"github.com/stripe/pg-schema-diff/pkg/diff"
)

// The name of a NOT NULL constraint is part of the schema on PostgreSQL 18, where pg_dump prints it
// whenever it is not the default "<table>_<column>_not_null". A partition's inherited constraint is
// the parent's fact and never produces a statement of its own; a change to a constraint the
// partition declares itself would need the partition detached and attached again, which the plan
// refuses rather than guesses.
var notNullConstraintCases = []acceptanceTestCase{
	{
		name: "rename a partitioned parent's NOT NULL constraint",
		oldSchemaDDL: []string{`
            CREATE TABLE foo (id int CONSTRAINT old_nn NOT NULL, tag text) PARTITION BY LIST (tag);
            CREATE TABLE foo_1 PARTITION OF foo FOR VALUES IN ('a');
		`},
		newSchemaDDL: []string{`
            CREATE TABLE foo (id int CONSTRAINT new_nn NOT NULL, tag text) PARTITION BY LIST (tag);
            CREATE TABLE foo_1 PARTITION OF foo FOR VALUES IN ('a');
		`},
		// The parent's rename cascades to the partition's inherited constraint; no statement may be
		// emitted for the partition itself.
	},
	{
		name: "rename a base table's NOT NULL constraint",
		oldSchemaDDL: []string{`
            CREATE TABLE foobar (id int CONSTRAINT old_nn NOT NULL);
		`},
		newSchemaDDL: []string{`
            CREATE TABLE foobar (id int CONSTRAINT new_nn NOT NULL);
		`},
	},
	{
		name: "name an existing default-named NOT NULL constraint",
		oldSchemaDDL: []string{`
            CREATE TABLE foobar (id int NOT NULL);
		`},
		newSchemaDDL: []string{`
            CREATE TABLE foobar (id int CONSTRAINT custom_nn NOT NULL);
		`},
	},
	{
		name: "mark a column NOT NULL with a named constraint",
		oldSchemaDDL: []string{`
            CREATE TABLE foobar (id int);
		`},
		newSchemaDDL: []string{`
            CREATE TABLE foobar (id int CONSTRAINT custom_nn NOT NULL);
		`},
	},
	{
		name: "rename a partition's locally declared NOT NULL constraint",
		oldSchemaDDL: []string{`
            CREATE TABLE foo (id int, tag text) PARTITION BY LIST (tag);
            CREATE TABLE foo_1 PARTITION OF foo (id NOT NULL) FOR VALUES IN ('a');
		`},
		newSchemaDDL: []string{`
            CREATE TABLE foo (id int, tag text) PARTITION BY LIST (tag);
            CREATE TABLE foo_1 PARTITION OF foo (id CONSTRAINT local_nn NOT NULL) FOR VALUES IN ('a');
		`},
		// Renaming the partition's own constraint on an attached partition is refused; before 18
		// the name is not part of the schema, so there is nothing to rename.
		expectedPlanErrorIs:       diff.ErrNotImplemented,
		expectedPlanErrorContains: `"public"."foo_1"`,
		planErrorOnlyAfterPG18:    true,
	},
	{
		name: "rename a partition's local NOT NULL constraint while the partition has a local primary key",
		oldSchemaDDL: []string{`
			CREATE TABLE foo (id int, tag text) PARTITION BY LIST (tag);
			CREATE TABLE foo_1 PARTITION OF foo (id NOT NULL, PRIMARY KEY (id, tag)) FOR VALUES IN ('a');
		`},
		newSchemaDDL: []string{`
			CREATE TABLE foo (id int, tag text) PARTITION BY LIST (tag);
			CREATE TABLE foo_1 PARTITION OF foo (id CONSTRAINT local_nn NOT NULL, PRIMARY KEY (id, tag)) FOR VALUES IN ('a');
		`},
		expectedPlanErrorIs:       diff.ErrNotImplemented,
		expectedPlanErrorContains: `"public"."foo_1"`,
		planErrorOnlyAfterPG18:    true,
	},
	{
		name: "replace a partition's local NOT NULL constraint with the parent's",
		oldSchemaDDL: []string{`
            CREATE TABLE foo (id int, tag text) PARTITION BY LIST (tag);
            CREATE TABLE foo_1 PARTITION OF foo (id NOT NULL) FOR VALUES IN ('a');
		`},
		newSchemaDDL: []string{`
            CREATE TABLE foo (id int NOT NULL, tag text) PARTITION BY LIST (tag);
            CREATE TABLE foo_1 PARTITION OF foo FOR VALUES IN ('a');
		`},
		// A local-to-inherited transition on an attached partition needs the detach/attach dance.
		expectedPlanErrorIs:       diff.ErrNotImplemented,
		expectedPlanErrorContains: `"public"."foo_1"`,
		planErrorOnlyAfterPG18:    true,
	},
	{
		name: "rename a partitioned parent's NOT NULL constraint while the partition gets a local name",
		oldSchemaDDL: []string{`
			CREATE TABLE foo (id int CONSTRAINT old_nn NOT NULL, tag text) PARTITION BY LIST (tag);
			CREATE TABLE foo_1 PARTITION OF foo FOR VALUES IN ('a');
		`},
		newSchemaDDL: []string{`
			CREATE TABLE foo (id int CONSTRAINT new_nn NOT NULL, tag text) PARTITION BY LIST (tag);
			CREATE TABLE foo_1 PARTITION OF foo (id CONSTRAINT local_nn NOT NULL) FOR VALUES IN ('a');
		`},
		expectedPlanErrorIs:       diff.ErrNotImplemented,
		expectedPlanErrorContains: `"public"."foo_1"`,
		planErrorOnlyAfterPG18:    true,
	},
	{
		name: "rename a partitioned parent's NOT NULL constraint while the partition keeps the old name",
		oldSchemaDDL: []string{`
            CREATE TABLE foo (id int CONSTRAINT old_nn NOT NULL, tag text) PARTITION BY LIST (tag);
            CREATE TABLE foo_1 PARTITION OF foo FOR VALUES IN ('a');
		`},
		newSchemaDDL: []string{`
            CREATE TABLE foo (id int CONSTRAINT new_nn NOT NULL, tag text) PARTITION BY LIST (tag);
            CREATE TABLE foo_1 PARTITION OF foo (id CONSTRAINT old_nn NOT NULL) FOR VALUES IN ('a');
		`},
		expectedPlanErrorIs:       diff.ErrNotImplemented,
		expectedPlanErrorContains: `"public"."foo_1"`,
		planErrorOnlyAfterPG18:    true,
	},
	{
		name: "partitioned parent gains NOT NULL while the partition declares its own",
		oldSchemaDDL: []string{`
			CREATE TABLE foo (id int, tag text) PARTITION BY LIST (tag);
			CREATE TABLE foo_1 PARTITION OF foo FOR VALUES IN ('a');
		`},
		newSchemaDDL: []string{`
			CREATE TABLE foo (id int NOT NULL, tag text) PARTITION BY LIST (tag);
			CREATE TABLE foo_1 PARTITION OF foo (id NOT NULL) FOR VALUES IN ('a');
		`},
		// The parent's constraint would cascade under the parent's name; the partition's own
		// constraint cannot simply be added beside it while attached. Before 18 no locality is
		// catalogued, and the names it would change are not part of the schema.
		expectedPlanErrorIs:       diff.ErrNotImplemented,
		expectedPlanErrorContains: `"public"."foo_1"`,
		planErrorOnlyAfterPG18:    true,
	},
}

func TestNotNullConstraintTestCases(t *testing.T) {
	runTestCases(t, notNullConstraintCases)
}
