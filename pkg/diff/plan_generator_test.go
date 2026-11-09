package diff

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v4/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"github.com/stripe/pg-schema-diff/internal/pgengine"
	"github.com/stripe/pg-schema-diff/internal/schema"
	"github.com/stripe/pg-schema-diff/pkg/log"
	externalschema "github.com/stripe/pg-schema-diff/pkg/schema"

	"github.com/stripe/pg-schema-diff/pkg/tempdb"
)

type fakeSchemaSource struct {
	t *testing.T

	expectedDeps schemaSourcePlanDeps
	schema       schema.Schema
	err          error
}

func (f fakeSchemaSource) GetSchema(_ context.Context, deps schemaSourcePlanDeps) (schema.Schema, error) {
	assert.Equal(f.t, f.expectedDeps.logger, deps.logger)
	assert.Equal(f.t, f.expectedDeps.tempDBFactory, deps.tempDBFactory)
	// We can't easily compare the function pointers, so we'll just assert the length of the slices.
	assert.Len(f.t, f.expectedDeps.getSchemaOpts, len(deps.getSchemaOpts))
	return f.schema, f.err
}

type planGeneratorTestSuite struct {
	suite.Suite

	pgEngine *pgengine.Engine
	db       *pgengine.DB
}

func (suite *planGeneratorTestSuite) mustGetTestDBPool() *sql.DB {
	pool, err := sql.Open("pgx", suite.db.GetDSN())
	suite.NoError(err)
	return pool
}

func (suite *planGeneratorTestSuite) mustBuildTempDbFactory(ctx context.Context) tempdb.Factory {
	tempDbFactory, err := tempdb.NewOnInstanceFactory(ctx, func(ctx context.Context, dbName string) (*sql.DB, error) {
		return sql.Open("pgx", suite.pgEngine.GetPostgresDatabaseConnOpts().With("dbname", dbName).ToDSN())
	})
	suite.Require().NoError(err)
	return tempDbFactory
}

func (suite *planGeneratorTestSuite) mustApplyDDLToTestDb(ddl []string) {
	conn := suite.mustGetTestDBPool()
	defer conn.Close()

	for _, stmt := range ddl {
		_, err := conn.Exec(stmt)
		suite.NoError(err)
	}
}

func (suite *planGeneratorTestSuite) SetupSuite() {
	engine, err := pgengine.StartEngine()
	suite.Require().NoError(err)
	suite.pgEngine = engine
}

func (suite *planGeneratorTestSuite) TearDownSuite() {
	suite.pgEngine.Close()
}

func (suite *planGeneratorTestSuite) SetupTest() {
	db, err := suite.pgEngine.CreateDatabase()
	suite.NoError(err)
	suite.db = db
}

func (suite *planGeneratorTestSuite) TearDownTest() {
	suite.db.DropDB()
}

func (suite *planGeneratorTestSuite) TestGenerate() {
	initialDDL := `
	CREATE TABLE foobar(
	    id CHAR(16) PRIMARY KEY
    ); `
	newSchemaDDL := `
	CREATE TABLE foobar(
	    id  CHAR(16) PRIMARY KEY,
		new_column VARCHAR(128) NOT NULL
    );
	`

	suite.mustApplyDDLToTestDb([]string{initialDDL})

	connPool := suite.mustGetTestDBPool()
	defer connPool.Close()

	tempDbFactory := suite.mustBuildTempDbFactory(context.Background())
	defer tempDbFactory.Close()

	plan, err := Generate(context.Background(), DBSchemaSource(connPool), DDLSchemaSource([]string{newSchemaDDL}), WithTempDbFactory(tempDbFactory))
	suite.NoError(err)

	suite.mustApplyMigrationPlan(connPool, plan)
	// Ensure that some sort of migration ran. we're really not testing the correctness of the
	// migration in this test suite
	_, err = connPool.ExecContext(context.Background(),
		"SELECT new_column FROM foobar;")
	suite.NoError(err)
}

func (suite *planGeneratorTestSuite) TestGeneratePlan_SchemaSourceErr() {
	tempDbFactory := suite.mustBuildTempDbFactory(context.Background())
	defer tempDbFactory.Close()

	logger := log.SimpleLogger()

	getSchemaOpts := []externalschema.GetSchemaOpt{
		externalschema.WithIncludeSchemas("schema_1"),
		externalschema.WithIncludeSchemas("schema_2"),
	}

	expectedErr := fmt.Errorf("some error")
	fakeSchemaSource := fakeSchemaSource{
		t: suite.T(),
		expectedDeps: schemaSourcePlanDeps{
			tempDBFactory: tempDbFactory,
			logger:        logger,
			getSchemaOpts: getSchemaOpts,
		},
		err: expectedErr,
	}

	connPool := suite.mustGetTestDBPool()
	defer connPool.Close()

	_, err := Generate(context.Background(), DBSchemaSource(connPool), fakeSchemaSource,
		WithTempDbFactory(tempDbFactory),
		WithGetSchemaOpts(getSchemaOpts...),
		WithLogger(logger),
	)
	suite.ErrorIs(err, expectedErr)
}

func (suite *planGeneratorTestSuite) mustApplyMigrationPlan(db *sql.DB, plan Plan) {
	// Run the migration
	for _, stmt := range plan.Statements {
		_, err := db.ExecContext(context.Background(), stmt.ToSQL())
		suite.Require().NoError(err)
	}
}

func (suite *planGeneratorTestSuite) TestGenerate_CannotPackNewTablesWithoutIgnoringChangesToColumnOrder() {
	tempDbFactory := suite.mustBuildTempDbFactory(context.Background())
	defer tempDbFactory.Close()

	connPool := suite.mustGetTestDBPool()
	defer connPool.Close()

	_, err := Generate(context.Background(), DBSchemaSource(connPool), DDLSchemaSource([]string{``}),
		WithTempDbFactory(tempDbFactory),
		WithDataPackNewTables(),
		WithRespectColumnOrder(),
	)
	suite.ErrorContains(err, "cannot data pack new tables without also ignoring changes to column order")
}

func (suite *planGeneratorTestSuite) TestGenerate_CannotBuildMigrationFromDDLWithoutTempDbFactory() {
	pool := suite.mustGetTestDBPool()
	defer pool.Close()
	_, err := Generate(context.Background(), DBSchemaSource(pool), DDLSchemaSource([]string{``}),
		WithIncludeSchemas("public"),
		WithDoNotValidatePlan(),
	)
	suite.ErrorContains(err, "tempDbFactory is required")
}

func (suite *planGeneratorTestSuite) TestGenerate_CannotValidateWithoutTempDbFactory() {
	pool := suite.mustGetTestDBPool()
	defer pool.Close()
	_, err := Generate(context.Background(), DBSchemaSource(pool), DDLSchemaSource([]string{``}),
		WithIncludeSchemas("public"),
		WithDoNotValidatePlan(),
	)
	suite.ErrorContains(err, "tempDbFactory is required")
}

func (suite *planGeneratorTestSuite) TestGenerate_RecreatedMaterializedViewComesBackPopulatedWithItsIndexes() {
	ctx := context.Background()
	oldDDL := []string{`
		CREATE TABLE foobar(id INT PRIMARY KEY, foo VARCHAR(255));
		INSERT INTO foobar VALUES (1, 'one'), (2, 'two');
		CREATE MATERIALIZED VIEW foobar_view AS
			SELECT id, foo FROM foobar WHERE id > 0;
		CREATE UNIQUE INDEX foobar_view_id_idx ON foobar_view(id) INCLUDE (foo);
		CREATE INDEX foobar_view_foo_idx ON foobar_view(foo);
	`}
	newDDL := []string{`
		CREATE TABLE foobar(id INT PRIMARY KEY, foo VARCHAR(255));
		CREATE MATERIALIZED VIEW foobar_view AS
			SELECT id, foo FROM foobar WHERE id >= 0;
		CREATE UNIQUE INDEX foobar_view_id_idx ON foobar_view(id) INCLUDE (foo);
		CREATE INDEX foobar_view_foo_idx ON foobar_view(foo);
	`}
	suite.mustApplyDDLToTestDb(oldDDL)

	connPool := suite.mustGetTestDBPool()
	defer connPool.Close()

	tempDbFactory := suite.mustBuildTempDbFactory(ctx)
	defer tempDbFactory.Close()

	plan, err := Generate(ctx, DBSchemaSource(connPool), DDLSchemaSource(newDDL), WithTempDbFactory(tempDbFactory))
	suite.Require().NoError(err)

	var planDDL []string
	for _, stmt := range plan.Statements {
		planDDL = append(planDDL, stmt.DDL)
	}
	suite.Contains(planDDL, `REFRESH MATERIALIZED VIEW "public"."foobar_view"`,
		"the re-created view must be populated in the same plan")
	suite.Contains(planDDL, `CREATE UNIQUE INDEX CONCURRENTLY foobar_view_id_idx ON public.foobar_view USING btree (id) INCLUDE (foo)`,
		"the re-created view must get its unique index back")
	suite.Contains(planDDL, `CREATE INDEX CONCURRENTLY foobar_view_foo_idx ON public.foobar_view USING btree (foo)`,
		"the re-created view must get its plain index back")

	suite.mustApplyMigrationPlan(connPool, plan)

	var isPopulated bool
	suite.Require().NoError(connPool.QueryRowContext(ctx, `
		SELECT c.relispopulated
		FROM pg_catalog.pg_class AS c
		INNER JOIN pg_catalog.pg_namespace AS n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relname = 'foobar_view'
	`).Scan(&isPopulated))
	suite.True(isPopulated, "the view must be populated after the plan")

	var rowCount int
	suite.Require().NoError(connPool.QueryRowContext(ctx, `SELECT COUNT(*) FROM foobar_view`).Scan(&rowCount))
	suite.Equal(2, rowCount, "the refresh must have run the new definition over the table's rows")

	rows, err := connPool.QueryContext(ctx, `
		SELECT indexname
		FROM pg_catalog.pg_indexes
		WHERE schemaname = 'public' AND tablename = 'foobar_view'
		ORDER BY indexname
	`)
	suite.Require().NoError(err)
	defer rows.Close()
	var indexNames []string
	for rows.Next() {
		var name string
		suite.Require().NoError(rows.Scan(&name))
		indexNames = append(indexNames, name)
	}
	suite.Require().NoError(rows.Err())
	suite.Equal([]string{"foobar_view_foo_idx", "foobar_view_id_idx"}, indexNames,
		"both indexes of the re-created view must exist after the plan")
}

func (suite *planGeneratorTestSuite) TestValidate_RejectsPlanThatLeavesRecreatedMaterializedViewUnpopulated() {
	ctx := context.Background()
	oldDDL := []string{`
		CREATE TABLE foobar(id INT PRIMARY KEY, foo VARCHAR(255));
		CREATE MATERIALIZED VIEW foobar_view AS
			SELECT id, foo FROM foobar WHERE id > 0;
		CREATE UNIQUE INDEX foobar_view_id_idx ON foobar_view(id);
	`}
	newDDL := []string{`
		CREATE TABLE foobar(id INT PRIMARY KEY, foo VARCHAR(255));
		CREATE MATERIALIZED VIEW foobar_view AS
			SELECT id, foo FROM foobar WHERE id >= 0;
		CREATE UNIQUE INDEX foobar_view_id_idx ON foobar_view(id);
	`}
	suite.mustApplyDDLToTestDb(oldDDL)

	currentPool := suite.mustGetTestDBPool()
	defer currentPool.Close()

	targetDB, err := suite.pgEngine.CreateDatabase()
	suite.Require().NoError(err)
	defer targetDB.DropDB()
	targetPool, err := sql.Open("pgx", targetDB.GetDSN())
	suite.Require().NoError(err)
	defer targetPool.Close()
	for _, stmt := range newDDL {
		_, err := targetPool.ExecContext(ctx, stmt)
		suite.Require().NoError(err)
	}

	currentSchema, err := schema.GetSchema(ctx, currentPool)
	suite.Require().NoError(err)
	targetSchema, err := schema.GetSchema(ctx, targetPool)
	suite.Require().NoError(err)
	suite.Require().Len(currentSchema.MaterializedViews, 1)
	suite.True(currentSchema.MaterializedViews[0].IsPopulated)
	suite.Require().Len(targetSchema.MaterializedViews, 1)
	suite.True(targetSchema.MaterializedViews[0].IsPopulated)

	tempDbFactory := suite.mustBuildTempDbFactory(ctx)
	defer tempDbFactory.Close()

	plan, err := Generate(ctx, DBSchemaSource(currentPool), DDLSchemaSource(newDDL),
		WithTempDbFactory(tempDbFactory), WithDoNotValidatePlan())
	suite.Require().NoError(err)

	var withoutRefresh []Statement
	for _, stmt := range plan.Statements {
		if strings.HasPrefix(stmt.DDL, "REFRESH MATERIALIZED VIEW") {
			continue
		}
		withoutRefresh = append(withoutRefresh, stmt)
	}
	suite.Require().Len(withoutRefresh, len(plan.Statements)-1,
		"the generated plan must refresh the re-created view exactly once")

	err = assertValidPlan(ctx, tempDbFactory, currentSchema, targetSchema, Plan{Statements: withoutRefresh}, &planOptions{
		logger:     log.SimpleLogger(),
		randReader: rand.Reader,
	})
	suite.Require().Error(err, "validation must reject a plan that leaves the re-created view unpopulated")
	suite.ErrorContains(err, "validating plan failed")
	suite.ErrorContains(err, `REFRESH MATERIALIZED VIEW "public"."foobar_view"`)
}

func (suite *planGeneratorTestSuite) TestGenerate_RefreshNamesUnreadableRelationWhenApplyingRoleCannotReadIt() {
	ctx := context.Background()
	adminPool := suite.mustGetTestDBPool()
	defer adminPool.Close()

	roleName := fmt.Sprintf("limited_refresh_%d", time.Now().UnixNano())
	_, err := adminPool.ExecContext(ctx, fmt.Sprintf("CREATE ROLE %s LOGIN", schema.EscapeIdentifier(roleName)))
	suite.Require().NoError(err)
	defer func() {
		_, _ = adminPool.ExecContext(ctx, fmt.Sprintf("DROP ROLE %s", schema.EscapeIdentifier(roleName)))
	}()

	// The applying role can create the view (CREATE ... WITH NO DATA reads nothing), but it
	// cannot read the table the view's query reads.
	_, err = adminPool.ExecContext(ctx, `CREATE TABLE public.secret_base(id INT)`)
	suite.Require().NoError(err)
	_, err = adminPool.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA work AUTHORIZATION %s", schema.EscapeIdentifier(roleName)))
	suite.Require().NoError(err)
	defer func() {
		_, _ = adminPool.ExecContext(ctx, "DROP SCHEMA work CASCADE")
	}()

	graph, err := newMaterializedViewSQLVertexGenerator().Add(schema.MaterializedView{
		SchemaQualifiedName: schema.SchemaQualifiedName{
			SchemaName:  "work",
			EscapedName: schema.EscapeIdentifier("mv"),
		},
		Owner:          roleName,
		ViewDefinition: "SELECT id FROM public.secret_base",
		IsPopulated:    true,
	})
	suite.Require().NoError(err)

	limitedPool, err := sql.Open("pgx", suite.db.GetConnOpts().With("user", roleName).ToDSN())
	suite.Require().NoError(err)
	defer limitedPool.Close()

	var planErr error
	for _, stmt := range graph.statements() {
		if _, err := limitedPool.ExecContext(ctx, stmt.DDL); err != nil {
			planErr = err
			break
		}
	}
	suite.Require().Error(planErr, "the refresh must fail when the applying role cannot read the view's relations")
	suite.ErrorContains(planErr, "permission denied for table secret_base",
		"the failure names the relation the applying role must be able to read")
}

func (suite *planGeneratorTestSuite) TestGenerate_ValidatesSchemaWithGeneratedColumns() {
	ctx := context.Background()
	connPool := suite.mustGetTestDBPool()
	defer connPool.Close()

	var serverVersionNum int
	suite.Require().NoError(connPool.QueryRowContext(ctx,
		`SELECT CURRENT_SETTING('server_version_num')::INT`).Scan(&serverVersionNum))

	// PostgreSQL 18 introduced virtual generated columns (attgenerated = 'v'). A schema that
	// carries one must survive plan validation: the tool reconstructs the current schema in a
	// temporary database, and reading a virtual column as an ordinary default made that CREATE
	// TABLE fail with "cannot use column reference in DEFAULT expression".
	storedColumn := `unclaimed_amount numeric GENERATED ALWAYS AS (amount * (capacity - filled)) STORED NOT NULL`
	virtualColumn := `open_amount numeric GENERATED ALWAYS AS (amount * (capacity - filled)) VIRTUAL`
	columnDefs := []string{
		"amount numeric NOT NULL",
		"capacity smallint NOT NULL",
		"filled smallint DEFAULT 0 NOT NULL",
		storedColumn,
	}
	if serverVersionNum >= 180000 {
		columnDefs = append(columnDefs, virtualColumn)
	}
	ddl := []string{fmt.Sprintf("CREATE TABLE generated_columns(\n\t%s\n)", strings.Join(columnDefs, ",\n\t"))}
	suite.mustApplyDDLToTestDb(ddl)

	tempDbFactory := suite.mustBuildTempDbFactory(ctx)
	defer tempDbFactory.Close()

	plan, err := Generate(ctx, DBSchemaSource(connPool), DDLSchemaSource(ddl), WithTempDbFactory(tempDbFactory))
	suite.Require().NoError(err, "plan validation must accept an unchanged schema with generated columns")
	suite.Empty(plan.Statements)

	fetched, err := schema.GetSchema(ctx, connPool)
	suite.Require().NoError(err)
	suite.Require().Len(fetched.Tables, 1)
	columnsByName := make(map[string]schema.Column)
	for _, column := range fetched.Tables[0].Columns {
		columnsByName[column.Name] = column
	}
	suite.Equal(schema.GeneratedColumnTypeStored, columnsByName["unclaimed_amount"].GeneratedColumnType)
	suite.NotEmpty(columnsByName["unclaimed_amount"].GenerationExpression)
	suite.Empty(columnsByName["unclaimed_amount"].Default)
	if serverVersionNum >= 180000 {
		suite.Equal(schema.GeneratedColumnTypeVirtual, columnsByName["open_amount"].GeneratedColumnType)
	}
}

func (suite *planGeneratorTestSuite) TestGenerate_ValidatesSchemaWithTriggerWhenComparingThreeColumnsAsARow() {
	ctx := context.Background()

	// PostgreSQL expands a row comparison of three or more columns into nested ORs of IS DISTINCT
	// FROM and prints the nested form, but parses that text back into a flat OR of the same terms.
	// Creating a trigger from the printed definition therefore prints differently the second time.
	ddl := []string{`
		CREATE TABLE foobar(id INT PRIMARY KEY, a INT, b INT, c INT);
		CREATE FUNCTION foobar_notify() RETURNS trigger AS $$ BEGIN RETURN NEW; END; $$ LANGUAGE plpgsql;
		CREATE CONSTRAINT TRIGGER foobar_trigger
			AFTER UPDATE ON foobar
			DEFERRABLE INITIALLY DEFERRED
			FOR EACH ROW
			WHEN ((OLD.a, OLD.b, OLD.c) IS DISTINCT FROM (NEW.a, NEW.b, NEW.c))
			EXECUTE FUNCTION foobar_notify();
	`}
	suite.mustApplyDDLToTestDb(ddl)

	connPool := suite.mustGetTestDBPool()
	defer connPool.Close()

	tempDbFactory := suite.mustBuildTempDbFactory(ctx)
	defer tempDbFactory.Close()

	// The source and the target are the same schema, so a plan is empty and validation has nothing
	// to change. It must not report the trigger's WHEN condition as a difference.
	plan, err := Generate(ctx, DBSchemaSource(connPool), DDLSchemaSource(ddl), WithTempDbFactory(tempDbFactory))
	suite.Require().NoError(err, "plan validation must accept an unchanged schema whose trigger compares three columns as a row")
	suite.Empty(plan.Statements)
}

func TestSimpleMigratorTestSuite(t *testing.T) {
	suite.Run(t, new(planGeneratorTestSuite))
}
