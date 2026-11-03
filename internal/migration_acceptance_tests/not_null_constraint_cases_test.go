package migration_acceptance_tests

import (
	"testing"

	"github.com/stripe/pg-schema-diff/pkg/diff"
)

// The name of a NOT NULL constraint is part of the schema on PostgreSQL 18, where pg_dump prints it
// whenever it is not the default "<table>_<column>_not_null". These cases cover every statement the
// plan can use to move a constraint to its target name.
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
		// The constraint is the partition's own, so it cannot be renamed in place: the plan detaches
		// the partition, renames and attaches it back.
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAcquiresAccessExclusiveLock,
		},
	},
}

func TestNotNullConstraintTestCases(t *testing.T) {
	runTestCases(t, notNullConstraintCases)
}
