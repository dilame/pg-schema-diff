package migration_acceptance_tests

import (
	"testing"

	"github.com/stripe/pg-schema-diff/pkg/diff"
)

var viewAcceptanceTestCases = []acceptanceTestCase{
	{
		name: "No-op",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo VARCHAR(255) NOT NULL,
                bar VARCHAR(255) UNIQUE,
                buzz BOOLEAN DEFAULT true
            );

            CREATE VIEW foobar_view AS
                SELECT id, foo, bar 
                FROM foobar 
                WHERE buzz = true;

            CREATE VIEW foobar_count AS
                SELECT COUNT(*) as total_count 
                FROM foobar;
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo VARCHAR(255) NOT NULL,
                bar VARCHAR(255) UNIQUE,
                buzz BOOLEAN DEFAULT true
            );

            CREATE VIEW foobar_view AS
                SELECT id, foo, bar 
                FROM foobar 
                WHERE buzz = true;

            CREATE VIEW foobar_count AS
                SELECT COUNT(*) as total_count 
                FROM foobar;
			`,
		},
		expectEmptyPlan: true,
	},
	{
		name: "Add view",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo VARCHAR(255) NOT NULL,
                bar DECIMAL(10,2),
                buzz INT
            );
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo VARCHAR(255) NOT NULL,
                bar DECIMAL(10,2),
                buzz INT
            );

            CREATE VIEW foobar_view AS
                SELECT id, foo, bar 
                FROM foobar 
                WHERE bar > 100.00;
			`,
		},
	},
	{
		name: "Add recursive view",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo VARCHAR(255) NOT NULL,
                parent_id INT REFERENCES foobar(id)
            );
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo VARCHAR(255) NOT NULL,
                parent_id INT REFERENCES foobar(id)
            );

            CREATE VIEW foobar_hierarchy AS
                WITH RECURSIVE hierarchy AS (
                    SELECT id, foo, parent_id, 0 as level
                    FROM foobar 
                    WHERE parent_id IS NULL
                    UNION ALL
                    SELECT f.id, f.foo, f.parent_id, h.level + 1
                    FROM foobar f
                    JOIN hierarchy h ON f.parent_id = h.id
                )
                SELECT * FROM hierarchy;
			`,
		},
	},
	{
		name:         "Add view with dependent table",
		oldSchemaDDL: nil,
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo INT NOT NULL,
                bar DECIMAL(10,2),
                buzz VARCHAR(50) DEFAULT 'pending'
            );

            CREATE VIEW foobar_view AS
                SELECT id, foo, bar 
                FROM foobar 
                WHERE buzz = 'pending';
			`,
		},
	},
	{
		name: "Add view with dependent column",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo VARCHAR(255) NOT NULL,
                bar VARCHAR(100)
            );
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo VARCHAR(255) NOT NULL,
                bar VARCHAR(100),
                buzz DECIMAL(10,2)
            );

            CREATE VIEW foobar_view AS
                SELECT id, foo, bar, buzz 
                FROM foobar;
			`,
		},
	},
	{
		name: "Drop view",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo VARCHAR(255),
                bar INT,
                buzz VARCHAR(100)
            );

            CREATE VIEW foobar_view AS
                SELECT id, foo, bar 
                FROM foobar 
                WHERE bar < 10;
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo VARCHAR(255),
                bar INT,
                buzz VARCHAR(100)
            );
			`,
		},
	},
	{
		name: "Drop view and underlying table",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo VARCHAR(255),
                bar TIMESTAMP DEFAULT CURRENT_TIMESTAMP
            );

            CREATE VIEW foobar_view AS
                SELECT id, foo, bar 
                FROM foobar 
                WHERE bar > CURRENT_DATE - INTERVAL '7 days';
			`,
		},
		newSchemaDDL: nil,
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeDeletesData,
		},
	},
	{
		name: "Drop view and underlying column",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo VARCHAR(255),
                bar INT,
                buzz VARCHAR(255),
                fizz TIMESTAMP DEFAULT CURRENT_TIMESTAMP
            );

            CREATE VIEW foobar_view AS
                SELECT id, foo, bar, buzz 
                FROM foobar;
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo VARCHAR(255),
                bar INT,
                fizz TIMESTAMP DEFAULT CURRENT_TIMESTAMP
            );
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeDeletesData,
		},
	},
	{
		name: "Recreate view due to table recreation (unpartitioned to partitioned)",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo INT,
                bar DECIMAL(10,2),
                buzz DATE
            );

            CREATE VIEW foobar_view AS
                SELECT DATE_TRUNC('month', buzz) as month, SUM(bar) as total
                FROM foobar 
                GROUP BY DATE_TRUNC('month', buzz);
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT,
                foo INT,
                bar DECIMAL(10,2),
                buzz DATE,
                PRIMARY KEY (id, buzz)
            ) PARTITION BY RANGE (buzz);

            CREATE VIEW foobar_view AS
                SELECT DATE_TRUNC('month', buzz) as month, SUM(bar) as total
                FROM foobar 
                GROUP BY DATE_TRUNC('month', buzz);
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeDeletesData,
		},
	},
	{
		name: "Recreate view due dependent table changing",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo VARCHAR(255),
                bar DECIMAL(10,2)
            );

            CREATE VIEW foobar_view AS
                SELECT foo, AVG(bar) as avg_bar
                FROM foobar 
                GROUP BY foo;
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE bar(
                id INT PRIMARY KEY,
                foo VARCHAR(255),
                bar DECIMAL(10,2)
            );

            CREATE VIEW foobar_view AS
                SELECT foo, AVG(bar) as avg_bar
                FROM bar 
                GROUP BY foo;
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeDeletesData,
		},
	},
	{
		name: "Recreate view due to dependent column changing",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                bar TIMESTAMP,
                foo VARCHAR(255),
                old_buzz VARCHAR(50)
            );

            CREATE VIEW foobar_view AS
                SELECT id, foo, old_buzz as buzz
                FROM foobar 
                WHERE old_buzz = 'active';
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                bar TIMESTAMP,
                foo VARCHAR(255),
                new_buzz VARCHAR(50)
            );

            CREATE VIEW foobar_view AS
                SELECT id, foo, new_buzz as buzz
                FROM foobar 
                WHERE new_buzz = 'active';
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeDeletesData,
		},
	},
	{
		name: "alter - add column and change condition",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                bar TIMESTAMP,
                foo VARCHAR(255)
            );

            CREATE VIEW foobar_view AS
                SELECT id, foo
                FROM foobar 
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                bar TIMESTAMP,
                foo VARCHAR(255)
            );

            CREATE VIEW foobar_view AS
                SELECT id, foo, bar
                FROM foobar 
                WHERE bar < CURRENT_TIMESTAMP
			`,
		},
	},
	{
		name: "alter - removing select column",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo VARCHAR(255),
                bar VARCHAR(255),
                buzz VARCHAR(255),
                fizz BOOLEAN DEFAULT true
            );

            CREATE VIEW foobar_view AS
                SELECT id, foo, bar 
                FROM foobar 
                WHERE fizz = true;
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo VARCHAR(255),
                bar VARCHAR(255),
                buzz VARCHAR(255),
                fizz BOOLEAN DEFAULT true
            );

            CREATE VIEW foobar_view AS
                SELECT foo, bar
                FROM foobar 
                WHERE fizz = true;
			`,
		},
	},
	{
		name: "alter - add security barrier",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo VARCHAR(255),
                bar INT,
                buzz TIMESTAMP DEFAULT CURRENT_TIMESTAMP
            );

            CREATE VIEW foobar_view AS
                SELECT foo, bar, buzz 
                FROM foobar 
                WHERE buzz > CURRENT_DATE - INTERVAL '1 day';
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo VARCHAR(255),
                bar INT,
                buzz TIMESTAMP DEFAULT CURRENT_TIMESTAMP
            );

            CREATE VIEW foobar_view WITH (security_barrier = true) AS
                SELECT foo, bar, buzz 
                FROM foobar 
                WHERE buzz > CURRENT_DATE - INTERVAL '1 day';
			`,
		},
	},
	{
		name: "alter - change check option",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo INT NOT NULL,
                bar VARCHAR(50) DEFAULT 'pending',
                buzz DECIMAL(10,2) CHECK (buzz > 0)
            );

            CREATE VIEW foobar_view AS
                SELECT id, foo, buzz 
                FROM foobar 
                WHERE bar = 'pending'
                WITH LOCAL CHECK OPTION;
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo INT NOT NULL,
                bar VARCHAR(50) DEFAULT 'pending',
                buzz DECIMAL(10,2) CHECK (buzz > 0)
            );

            CREATE VIEW foobar_view AS
                SELECT id, foo, buzz 
                FROM foobar 
                WHERE bar = 'pending'
                WITH CASCADED CHECK OPTION;
			`,
		},
	},
	{
		name: "alter - add check option",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo VARCHAR(255) NOT NULL,
                bar DECIMAL(10,2) CHECK (bar > 0),
                buzz BOOLEAN DEFAULT true
            );

            CREATE VIEW foobar_view AS
                SELECT id, foo, bar 
                FROM foobar 
                WHERE buzz = true;
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo VARCHAR(255) NOT NULL,
                bar DECIMAL(10,2) CHECK (bar > 0),
                buzz BOOLEAN DEFAULT true
            );

            CREATE VIEW foobar_view AS
                SELECT id, foo, bar 
                FROM foobar 
                WHERE buzz = true
                WITH CHECK OPTION;
			`,
		},
	},
	{
		name: "alter - remove check option",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo VARCHAR(255),
                bar INT CHECK (bar >= 0),
                buzz BOOLEAN DEFAULT true
            );

            CREATE VIEW foobar_view AS
                SELECT id, foo, bar 
                FROM foobar 
                WHERE buzz = true
                WITH CHECK OPTION;
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo VARCHAR(255),
                bar INT CHECK (bar >= 0),
                buzz BOOLEAN DEFAULT true
            );

            CREATE VIEW foobar_view AS
                SELECT id, foo, bar
                FROM foobar
                WHERE buzz = true;
			`,
		},
	},
	{
		name: "create a view that reads another view created in the same plan",
		oldSchemaDDL: []string{
			``,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(id INT, foo VARCHAR(255));
            CREATE VIEW foobar_view AS SELECT id, foo FROM foobar;
            CREATE VIEW foobar_view_view AS SELECT id FROM foobar_view;
			`,
		},
		// The outer view's defining query is resolved at CREATE time, and it reads a view: the
		// dependency kind, not just the name, decides which vertex the dependency is emitted by.
		expectedPlanDDL: []string{
			"CREATE TABLE \"public\".\"foobar\" (\n\t\"id\" integer,\n\t\"foo\" character varying(255) COLLATE \"pg_catalog\".\"default\"\n)",
			"ALTER TABLE \"public\".\"foobar\" OWNER TO \"postgres\"",
			"CREATE VIEW \"public\".\"foobar_view\" AS\n SELECT id,\n    foo\n   FROM foobar;",
			"ALTER VIEW \"public\".\"foobar_view\" OWNER TO \"postgres\"",
			"CREATE VIEW \"public\".\"foobar_view_view\" AS\n SELECT id\n   FROM foobar_view;",
			"ALTER VIEW \"public\".\"foobar_view_view\" OWNER TO \"postgres\"",
		},
	},
	{
		name: "no-op - view definition with an unnamed union column",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY
            );

            CREATE VIEW foobar_view AS
                SELECT x.id, x.kind
                FROM (
                    SELECT id, 'credit'::text AS kind FROM foobar
                    UNION ALL
                    SELECT id, 'incentive' FROM foobar
                ) x;
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY
            );

            CREATE VIEW foobar_view AS
                SELECT x.id, x.kind
                FROM (
                    SELECT id, 'credit'::text AS kind FROM foobar
                    UNION ALL
                    SELECT id, 'incentive' FROM foobar
                ) x;
			`,
		},
		// The second UNION branch is an untyped literal, so pg_get_viewdef
		// deparses the view without naming it, and re-creating the view from that
		// text names it "text". The two databases hold the same view under two
		// texts; validation must not read that as a change.
		expectEmptyPlan: true,
	},
	{
		name: "change a view definition in place when an unchanged view reads it",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(id INT, foo INT);

            CREATE VIEW foobar_view AS
                SELECT id, foo
                FROM foobar;

            CREATE VIEW foobar_view_view AS
                SELECT id
                FROM foobar_view;
		`},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(id INT, foo INT);

            CREATE VIEW foobar_view AS
                SELECT id, foo + 0 AS foo
                FROM foobar;

            CREATE VIEW foobar_view_view AS
                SELECT id
                FROM foobar_view;
		`},
		// The outer view is unchanged and reads the inner one, so the inner view's change has to be
		// applied with CREATE OR REPLACE VIEW: a DROP would fail while the outer view depends on it.
		expectedPlanDDL: []string{
			"CREATE OR REPLACE VIEW \"public\".\"foobar_view\" AS\n SELECT id,\n    foo + 0 AS foo\n   FROM foobar;",
		},
	},
	{
		name: "re-create a view and the unchanged view that reads it when its columns change",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(id INT, foo INT, bar INT);

            CREATE VIEW foobar_view AS
                SELECT id, foo, bar
                FROM foobar;

            CREATE VIEW foobar_view_view AS
                SELECT id
                FROM foobar_view;
		`},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(id INT, foo INT, bar INT);

            CREATE VIEW foobar_view AS
                SELECT id, foo
                FROM foobar;

            CREATE VIEW foobar_view_view AS
                SELECT id
                FROM foobar_view;
		`},
		// A removed output column forces the inner view to be dropped and created again, so the
		// unchanged outer view that reads it has to be dropped first and created again afterwards.
	},
	{
		name: "no-op - recursive view",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                parent_id INT REFERENCES foobar(id)
            );

            CREATE VIEW foobar_hierarchy AS
                WITH RECURSIVE hierarchy AS (
                    SELECT id, parent_id, 'root'::text AS kind
                    FROM foobar
                    WHERE parent_id IS NULL
                    UNION ALL
                    SELECT f.id, f.parent_id, 'child'
                    FROM foobar f
                    JOIN hierarchy h ON f.parent_id = h.id
                )
                SELECT * FROM hierarchy;
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                parent_id INT REFERENCES foobar(id)
            );

            CREATE VIEW foobar_hierarchy AS
                WITH RECURSIVE hierarchy AS (
                    SELECT id, parent_id, 'root'::text AS kind
                    FROM foobar
                    WHERE parent_id IS NULL
                    UNION ALL
                    SELECT f.id, f.parent_id, 'child'
                    FROM foobar f
                    JOIN hierarchy h ON f.parent_id = h.id
                )
                SELECT * FROM hierarchy;
			`,
		},
		expectEmptyPlan: true,
	},
	{
		name:  "replace a view in place when it moves onto a view created in the same plan",
		roles: []string{"reader"},
		oldSchemaDDL: []string{
			`
            CREATE SCHEMA a_door;
            CREATE SCHEMA z_projection;
            CREATE TABLE foobar(id INT, foo INT, hidden INT);

            CREATE VIEW a_door.foobar WITH (security_barrier = true) AS
                SELECT id, foo
                FROM foobar;
            GRANT SELECT ON a_door.foobar TO reader;
            COMMENT ON VIEW a_door.foobar IS 'the door';
            COMMENT ON COLUMN a_door.foobar.foo IS 'the foo';
		`},
		newSchemaDDL: []string{
			`
            CREATE SCHEMA a_door;
            CREATE SCHEMA z_projection;
            CREATE TABLE foobar(id INT, foo INT, hidden INT);

            CREATE VIEW z_projection.foobar AS
                SELECT id, foo
                FROM foobar;

            CREATE VIEW a_door.foobar WITH (security_barrier = true) AS
                SELECT id, foo
                FROM z_projection.foobar;
            GRANT SELECT ON a_door.foobar TO reader;
            COMMENT ON VIEW a_door.foobar IS 'the door';
            COMMENT ON COLUMN a_door.foobar.foo IS 'the foo';
		`},
		// The door keeps its output columns, so it is replaced in place even though the relation it
		// reads changed: its grant, comments and options are untouched and no reader of the door sees
		// it missing. The projection it now reads is created first, although the door's schema sorts
		// before the projection's.
		expectedPlanDDL: []string{
			"CREATE VIEW \"z_projection\".\"foobar\" AS\n SELECT id,\n    foo\n   FROM foobar;",
			"ALTER VIEW \"z_projection\".\"foobar\" OWNER TO \"postgres\"",
			"CREATE OR REPLACE VIEW \"a_door\".\"foobar\" WITH (security_barrier=true) AS\n SELECT id,\n    foo\n   FROM z_projection.foobar;",
		},
	},
	{
		name:  "replace a view in place when it stops reading a view dropped in the same plan",
		roles: []string{"reader"},
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(id INT, foo INT);

            CREATE VIEW z_projection AS
                SELECT id, foo
                FROM foobar;

            CREATE VIEW a_door AS
                SELECT id, foo
                FROM z_projection;
            GRANT SELECT ON a_door TO reader;
		`},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(id INT, foo INT);

            CREATE VIEW a_door AS
                SELECT id, foo
                FROM foobar;
            GRANT SELECT ON a_door TO reader;
		`},
		// The door is replaced in place before the view it used to read is dropped.
		expectedPlanDDL: []string{
			"CREATE OR REPLACE VIEW \"public\".\"a_door\" AS\n SELECT id,\n    foo\n   FROM foobar;",
			"DROP VIEW \"public\".\"z_projection\"",
		},
	},
	{
		name:  "re-create a view whose column type changes with its grants, comment, options and owner, and the view that reads it with its own",
		roles: []string{"reader", "view_owner"},
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(id INT, foo INT);

            CREATE VIEW inner_view WITH (security_barrier = true, security_invoker = true) AS
                SELECT id, foo
                FROM foobar;
            ALTER VIEW inner_view OWNER TO view_owner;
            GRANT SELECT ON inner_view TO reader;
            COMMENT ON VIEW inner_view IS 'inner';
            COMMENT ON COLUMN inner_view.id IS 'the id';

            CREATE VIEW outer_view WITH (security_barrier = true) AS
                SELECT id
                FROM inner_view;
            ALTER VIEW outer_view OWNER TO view_owner;
            GRANT SELECT ON outer_view TO reader;
            COMMENT ON VIEW outer_view IS 'outer';
            COMMENT ON COLUMN outer_view.id IS 'the outer id';
		`},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(id INT, foo INT);

            CREATE VIEW inner_view WITH (security_barrier = true, security_invoker = true) AS
                SELECT id::BIGINT AS id, foo
                FROM foobar;
            ALTER VIEW inner_view OWNER TO view_owner;
            GRANT SELECT ON inner_view TO reader;
            COMMENT ON VIEW inner_view IS 'inner';
            COMMENT ON COLUMN inner_view.id IS 'the id';

            CREATE VIEW outer_view WITH (security_barrier = true) AS
                SELECT id
                FROM inner_view;
            ALTER VIEW outer_view OWNER TO view_owner;
            GRANT SELECT ON outer_view TO reader;
            COMMENT ON VIEW outer_view IS 'outer';
            COMMENT ON COLUMN outer_view.id IS 'the outer id';
		`},
		// A retyped column cannot be replaced in place, so both views are dropped and created again,
		// and each comes back with everything it carried: the database after the plan matches the
		// target's dump, grants, comments, options and owners included.
	},
	{
		name: "comment on a view column",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(id INT, foo INT);
            CREATE VIEW foobar_view AS SELECT id, foo FROM foobar;
            COMMENT ON COLUMN foobar_view.id IS 'old';
		`},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(id INT, foo INT);
            CREATE VIEW foobar_view AS SELECT id, foo FROM foobar;
            COMMENT ON COLUMN foobar_view.foo IS 'new';
		`},
		expectedPlanDDL: []string{
			"COMMENT ON COLUMN \"public\".\"foobar_view\".\"id\" IS NULL",
			"COMMENT ON COLUMN \"public\".\"foobar_view\".\"foo\" IS 'new'",
		},
	},
	{
		name:  "re-create a view that moves off a dropped view onto a table that gains the column it reads",
		roles: []string{"reader"},
		oldSchemaDDL: []string{
			`
            CREATE TABLE t(id INT);
            CREATE VIEW w AS SELECT id FROM t;
            CREATE VIEW v AS SELECT id FROM w;
            GRANT SELECT ON v TO reader;
            COMMENT ON VIEW v IS 'v';
		`},
		newSchemaDDL: []string{
			`
            CREATE TABLE t(id INT, foo INT);
            CREATE VIEW v AS SELECT id, foo FROM t;
            GRANT SELECT ON v TO reader;
            COMMENT ON VIEW v IS 'v';
		`},
		// Replacing v in place would have to run after t gains foo and before w is dropped, while w
		// has to be dropped before t is altered. The plan re-creates v instead, with its grant and
		// comment.
	},
	{
		name:  "re-create a view that moves off a re-created view onto a table that gains the column it reads",
		roles: []string{"reader"},
		oldSchemaDDL: []string{
			`
            CREATE TABLE t(id INT);
            CREATE VIEW w AS SELECT id FROM t;
            CREATE VIEW v AS SELECT id FROM w;
            GRANT SELECT ON v TO reader;
            COMMENT ON VIEW v IS 'v';
		`},
		newSchemaDDL: []string{
			`
            CREATE TABLE t(id INT, foo INT);
            CREATE VIEW w AS SELECT id::BIGINT AS id FROM t;
            CREATE VIEW v AS SELECT id, foo FROM t;
            GRANT SELECT ON v TO reader;
            COMMENT ON VIEW v IS 'v';
		`},
	},
	{
		name:  "re-create a view that moves off a dropped view onto a new view over a table that gains a column",
		roles: []string{"reader"},
		oldSchemaDDL: []string{
			`
            CREATE TABLE t(id INT);
            CREATE VIEW w AS SELECT id FROM t;
            CREATE VIEW v AS SELECT id FROM w;
            GRANT SELECT ON v TO reader;
            COMMENT ON VIEW v IS 'v';
		`},
		newSchemaDDL: []string{
			`
            CREATE TABLE t(id INT, foo INT);
            CREATE VIEW n AS SELECT id, foo FROM t;
            CREATE VIEW v AS SELECT id FROM n;
            GRANT SELECT ON v TO reader;
            COMMENT ON VIEW v IS 'v';
		`},
	},
	{
		name:  "re-create the views over a table column whose type changes, with their state",
		roles: []string{"reader"},
		oldSchemaDDL: []string{
			`
            CREATE TABLE t(id INT, foo INT);
            CREATE VIEW v WITH (security_barrier = true) AS SELECT id FROM t;
            GRANT SELECT ON v TO reader;
            COMMENT ON VIEW v IS 'v';
            COMMENT ON COLUMN v.id IS 'the id';
            CREATE VIEW vv AS SELECT id FROM v;
            GRANT SELECT ON vv TO reader;
            CREATE MATERIALIZED VIEW mv AS SELECT id FROM t;
            GRANT SELECT ON mv TO reader;
            COMMENT ON MATERIALIZED VIEW mv IS 'mv';
		`},
		newSchemaDDL: []string{
			`
            CREATE TABLE t(id BIGINT, foo INT);
            CREATE VIEW v WITH (security_barrier = true) AS SELECT id FROM t;
            GRANT SELECT ON v TO reader;
            COMMENT ON VIEW v IS 'v';
            COMMENT ON COLUMN v.id IS 'the id';
            CREATE VIEW vv AS SELECT id FROM v;
            GRANT SELECT ON vv TO reader;
            CREATE MATERIALIZED VIEW mv AS SELECT id FROM t;
            GRANT SELECT ON mv TO reader;
            COMMENT ON MATERIALIZED VIEW mv IS 'mv';
		`},
		// PostgreSQL refuses to change the type of a column a view reads, so the views and the
		// materialized view over it, and the view over those, are dropped before the ALTER and
		// created again after it with their grants, comments and options.
		expectedHazardTypes: []diff.MigrationHazardType{diff.MigrationHazardTypeAcquiresAccessExclusiveLock, diff.MigrationHazardTypeImpactsDatabasePerformance},
	},
}

func TestViewTestCases(t *testing.T) {
	runTestCases(t, viewAcceptanceTestCases)
}
