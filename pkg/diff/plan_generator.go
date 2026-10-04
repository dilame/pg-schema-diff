package diff

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v4/stdlib"
	"github.com/kr/pretty"
	"github.com/stripe/pg-schema-diff/internal/graph"
	"github.com/stripe/pg-schema-diff/internal/schema"
	externalschema "github.com/stripe/pg-schema-diff/pkg/schema"

	"github.com/stripe/pg-schema-diff/pkg/log"
	"github.com/stripe/pg-schema-diff/pkg/tempdb"
)

const (
	tempDbMaxConnections = 5
)

var (
	errTempDbFactoryRequired = fmt.Errorf("tempDbFactory is required. include the option WithTempDbFactory")
)

type (
	planOptions struct {
		tempDbFactory              tempdb.Factory
		dataPackNewTables          bool
		ignoreChangesToColOrder    bool
		logger                     log.Logger
		validatePlan               bool
		getSchemaOpts              []schema.GetSchemaOpt
		randReader                 io.Reader
		noConcurrentIndexOps       bool
		disableCheckFunctionBodies bool
	}

	PlanOpt func(opts *planOptions)
)

func WithTempDbFactory(factory tempdb.Factory) PlanOpt {
	return func(opts *planOptions) {
		opts.tempDbFactory = factory
	}
}

// WithDataPackNewTables configures the plan generation such that it packs the columns in the new tables to minimize
// padding. It will help minimize the storage used by the tables
func WithDataPackNewTables() PlanOpt {
	return func(opts *planOptions) {
		opts.dataPackNewTables = true
	}
}

// WithRespectColumnOrder configures the plan generation to respect any changes to the ordering of columns in
// existing tables. You will most likely want this disabled, since column ordering changes are common
func WithRespectColumnOrder() PlanOpt {
	return func(opts *planOptions) {
		opts.ignoreChangesToColOrder = false
	}
}

// WithDoNotValidatePlan disables plan validation, where the migration plan is tested against a temporary database
// instance.
func WithDoNotValidatePlan() PlanOpt {
	return func(opts *planOptions) {
		opts.validatePlan = false
	}
}

// WithDisableCheckFunctionBodies runs plan validation with the check_function_bodies session setting off, so
// PostgreSQL does not resolve the references a routine's body makes at CREATE time. The default is on, which is how
// the migration will run; use this only when the plan creates a routine whose body names an object the plan cannot
// order before it.
func WithDisableCheckFunctionBodies() PlanOpt {
	return func(opts *planOptions) {
		opts.disableCheckFunctionBodies = true
	}
}

// WithLogger configures plan generation to use the provided logger instead of the default
func WithLogger(logger log.Logger) PlanOpt {
	return func(opts *planOptions) {
		opts.logger = logger
	}
}

func WithIncludeSchemas(schemas ...string) PlanOpt {
	return func(opts *planOptions) {
		opts.getSchemaOpts = append(opts.getSchemaOpts, schema.WithIncludeSchemas(schemas...))
	}
}

func WithExcludeSchemas(schemas ...string) PlanOpt {
	return func(opts *planOptions) {
		opts.getSchemaOpts = append(opts.getSchemaOpts, schema.WithExcludeSchemas(schemas...))
	}
}

func WithGetSchemaOpts(getSchemaOpts ...externalschema.GetSchemaOpt) PlanOpt {
	return func(opts *planOptions) {
		opts.getSchemaOpts = append(opts.getSchemaOpts, getSchemaOpts...)
	}
}

// WithRandReader seeds the random used to generate random SQL identifiers, e.g., temporary not-null check constraints.
func WithRandReader(randReader io.Reader) PlanOpt {
	return func(opts *planOptions) {
		opts.randReader = randReader
	}
}

// WithNoConcurrentIndexOps disables the use of CONCURRENTLY in CREATE INDEX and DROP INDEX statements.
// This can be useful when you need simpler DDL statements or when working in environments that don't support
// concurrent index operations. Note that disabling concurrent operations may result in longer lock times
// and potential downtime during migrations.
func WithNoConcurrentIndexOps() PlanOpt {
	return func(opts *planOptions) {
		opts.noConcurrentIndexOps = true
	}
}

// Generate generates a migration plan to migrate the database to the target schema
//
// Parameters:
// fromSchema:		The target schema to generate the diff for.
// targetSchema:	The (source of the) schema you want to migrate the database to. Use DDLSchemaSource if the new
// schema is encoded in DDL.
// opts: 			Additional options to configure the plan generation
func Generate(
	ctx context.Context,
	fromSchema SchemaSource,
	targetSchema SchemaSource,
	opts ...PlanOpt,
) (Plan, error) {
	planOptions := &planOptions{
		validatePlan:            true,
		ignoreChangesToColOrder: true,
		logger:                  log.SimpleLogger(),
		randReader:              rand.Reader,
	}
	for _, opt := range opts {
		opt(planOptions)
	}

	currentSchema, err := fromSchema.GetSchema(ctx, schemaSourcePlanDeps{
		tempDBFactory: planOptions.tempDbFactory,
		logger:        planOptions.logger,
		getSchemaOpts: planOptions.getSchemaOpts,
	})
	if err != nil {
		return Plan{}, fmt.Errorf("getting current schema: %w", err)
	}
	newSchema, err := targetSchema.GetSchema(ctx, schemaSourcePlanDeps{
		tempDBFactory: planOptions.tempDbFactory,
		logger:        planOptions.logger,
		getSchemaOpts: planOptions.getSchemaOpts,
	})
	if err != nil {
		return Plan{}, fmt.Errorf("getting new schema: %w", err)
	}

	statements, err := generateMigrationStatements(currentSchema, newSchema, planOptions)
	if err != nil {
		return Plan{}, fmt.Errorf("generating plan statements: %w", err)
	}

	hash, err := currentSchema.Hash()
	if err != nil {
		return Plan{}, fmt.Errorf("generating current schema hash: %w", err)
	}

	plan := Plan{
		Statements:        statements,
		CurrentSchemaHash: hash,
	}

	if planOptions.validatePlan {
		if planOptions.tempDbFactory == nil {
			return Plan{}, fmt.Errorf("cannot validate plan without a tempDbFactory: %w", errTempDbFactoryRequired)
		}
		if err := assertValidPlan(ctx, planOptions.tempDbFactory, currentSchema, newSchema, plan, planOptions); err != nil {
			return Plan{}, fmt.Errorf("validating migration plan: %w \n%# v", err, pretty.Formatter(plan))
		}
	}

	return plan, nil
}

// generateMigrationStatements orders the plan's statements. A view whose output columns stay the
// same is replaced in place, which keeps it readable throughout, but the replacement has to run
// after the relations its new definition reads and before the ones only its old definition reads
// are dropped, and the rest of the plan can make that order impossible (an old relation that is
// dropped before a new one can be altered). When the order has a cycle, every view replaced in
// place that lies on it is re-created instead, which the plan can always order, and the statements
// are ordered again. A re-created view comes back with all of its state, so the plan is correct
// either way.
func generateMigrationStatements(oldSchema, newSchema schema.Schema, planOptions *planOptions) ([]Statement, error) {
	recreatedViews := make(map[string]bool)
	for {
		statements, err := generateMigrationStatementsRecreatingViews(oldSchema, newSchema, planOptions, recreatedViews)
		var cycleErr *graph.CycleError
		if err == nil || !errors.As(err, &cycleErr) {
			return statements, err
		}
		if !recreateViewsOnCycle(oldSchema, newSchema, cycleErr.OnCycle, recreatedViews) {
			return nil, err
		}
	}
}

// recreateViewsOnCycle adds to recreatedViews every view the plan replaces in place whose
// replacement lies on a cycle, and reports whether it added any.
func recreateViewsOnCycle(oldSchema, newSchema schema.Schema, onCycle []string, recreatedViews map[string]bool) bool {
	onCycleIds := make(map[string]bool)
	for _, id := range onCycle {
		onCycleIds[id] = true
	}
	oldViewsByName := buildSchemaObjByNameMap(oldSchema.Views)
	added := false
	for _, newView := range newSchema.Views {
		oldView, ok := oldViewsByName[newView.GetName()]
		if !ok || recreatedViews[newView.GetName()] || !viewDefinitionChanged(oldView, newView) {
			continue
		}
		if onCycleIds[buildTableVertexId(newView.SchemaQualifiedName, diffTypeAddAlter).String()] {
			recreatedViews[newView.GetName()] = true
			added = true
		}
	}
	return added
}

func generateMigrationStatementsRecreatingViews(
	oldSchema, newSchema schema.Schema,
	planOptions *planOptions,
	recreatedViews map[string]bool,
) ([]Statement, error) {
	diff, _, err := buildSchemaDiff(oldSchema, newSchema, recreatedViews)
	if err != nil {
		return nil, err
	}

	if planOptions.dataPackNewTables {
		// Instead of enabling ignoreChangesToColOrder by default, force the user to enable ignoreChangesToColOrder.
		// This ensures the user knows what's going on behind-the-scenes
		if !planOptions.ignoreChangesToColOrder {
			return nil, fmt.Errorf("cannot data pack new tables without also ignoring changes to column order")
		}
		diff = dataPackNewTables(diff)
	}
	if planOptions.ignoreChangesToColOrder {
		diff = removeChangesToColumnOrdering(diff)
	}

	statements, err := newSchemaSQLGenerator(planOptions.randReader, planOptions).Alter(diff)
	if err != nil {
		return nil, fmt.Errorf("generating migration statements: %w", err)
	}
	return statements, nil
}

func assertValidPlan(ctx context.Context,
	tempDbFactory tempdb.Factory,
	currentSchema, newSchema schema.Schema,
	plan Plan,
	planOptions *planOptions,
) error {
	tempDb, err := tempDbFactory.Create(ctx)
	if err != nil {
		return err
	}
	defer func(closer tempdb.ContextualCloser) {
		if err := closer.Close(ctx); err != nil {
			planOptions.logger.Errorf("an error occurred while dropping the temp database: %s", err)
		}
	}(tempDb.ContextualCloser)
	// Set a max connections if a user has not set one. This is to prevent us from exploding the number of connections
	// on the database.
	setMaxConnectionsIfNotSet(tempDb.ConnPool, tempDbMaxConnections)

	if err := setSchemaForEmptyDatabase(ctx, tempDb, currentSchema, planOptions); err != nil {
		return fmt.Errorf("inserting schema in temporary database: %w", err)
	}

	if err := executeStatementsIgnoreTimeouts(ctx, tempDb.ConnPool, plan.Statements, planOptions.disableCheckFunctionBodies); err != nil {
		return fmt.Errorf("running migration plan: %w", err)
	}

	migratedSchema, err := schemaFromTempDb(ctx, tempDb, planOptions)
	if err != nil {
		return fmt.Errorf("fetching schema from migrated database: %w", err)
	}

	return assertMigratedSchemaMatchesTarget(migratedSchema, newSchema, planOptions)
}

func setMaxConnectionsIfNotSet(db *sql.DB, defaultMax int) {
	if db.Stats().MaxOpenConnections <= 0 {
		db.SetMaxOpenConns(defaultMax)
	}
}

func setSchemaForEmptyDatabase(ctx context.Context, emptyDb *tempdb.Database, targetSchema schema.Schema, options *planOptions) error {
	// We can't create invalid indexes. We'll mark them valid in the schema, which should be functionally
	// equivalent for the sake of DDL and other statements.
	//
	// Make a new array, so we don't mutate the underlying array of the original schema. Ideally, we have a clone function
	// in the future
	var validIndexes []schema.Index
	for _, idx := range targetSchema.Indexes {
		idx.IsInvalid = false
		validIndexes = append(validIndexes, idx)
	}
	targetSchema.Indexes = validIndexes

	// An empty database doesn't necessarily have an empty schema, so we should fetch it.
	startingSchema, err := schemaFromTempDb(ctx, emptyDb, options)
	if err != nil {
		return fmt.Errorf("getting schema from empty database: %w", err)
	}

	statements, err := generateMigrationStatements(startingSchema, targetSchema, &planOptions{})
	if err != nil {
		return fmt.Errorf("building schema diff: %w", err)
	}
	// This reconstructs the schema that already exists in the source database, and the tool only has
	// to reach the same state, not the same statement order. A legacy string-body routine records no
	// reference to the relation its body reads, so its create cannot be ordered after that relation;
	// turn the body check off here, as pg_dump does, so a source schema that still has such routines
	// can be reconstructed. The plan itself is still applied with the caller's setting (see
	// assertValidPlan), which is on by default.
	if err := executeStatementsIgnoreTimeouts(ctx, emptyDb.ConnPool, statements, true); err != nil {
		return fmt.Errorf("executing statements: %w\n%# v", err, pretty.Formatter(statements))
	}
	return nil
}

func schemaFromTempDb(ctx context.Context, db *tempdb.Database, plan *planOptions) (schema.Schema, error) {
	return schema.GetSchema(ctx, db.ConnPool, append(plan.getSchemaOpts, db.ExcludeMetadataOptions...)...)
}

// clearSkippedPrivileges returns a copy of the schema with all privileges cleared that are emitted
// as SkipValidation statements (table, column, schema, view, materialized view, routine, and
// default privileges).
// This is used during plan validation because privilege statements are skipped (roles don't exist in temp DB).
func clearSkippedPrivileges(s schema.Schema) schema.Schema {
	s.DefaultPrivileges = nil

	namedSchemas := make([]schema.NamedSchema, len(s.NamedSchemas))
	for i, namedSchema := range s.NamedSchemas {
		namedSchema.Privileges = nil
		namedSchemas[i] = namedSchema
	}
	s.NamedSchemas = namedSchemas

	tables := make([]schema.Table, len(s.Tables))
	for i, t := range s.Tables {
		t.Privileges = nil
		t.ColumnPrivileges = nil
		tables[i] = t
	}
	s.Tables = tables

	views := make([]schema.View, len(s.Views))
	for i, v := range s.Views {
		v.Privileges = nil
		views[i] = v
	}
	s.Views = views

	materializedViews := make([]schema.MaterializedView, len(s.MaterializedViews))
	for i, mv := range s.MaterializedViews {
		mv.Privileges = nil
		materializedViews[i] = mv
	}
	s.MaterializedViews = materializedViews

	functions := make([]schema.Function, len(s.Functions))
	for i, f := range s.Functions {
		f.Privileges = nil
		functions[i] = f
	}
	s.Functions = functions

	procedures := make([]schema.Procedure, len(s.Procedures))
	for i, p := range s.Procedures {
		p.Privileges = nil
		procedures[i] = p
	}
	s.Procedures = procedures

	return s
}

func assertMigratedSchemaMatchesTarget(migratedSchema, targetSchema schema.Schema, planOptions *planOptions) error {
	// Clear privileges from both schemas since privilege statements are skipped during validation
	// (roles don't exist in temp DB). We make copies to avoid modifying the original schemas.
	migratedSchema = clearSkippedPrivileges(migratedSchema)
	targetSchema = clearSkippedPrivileges(targetSchema)

	toTargetSchemaStmts, err := generateMigrationStatements(migratedSchema, targetSchema, planOptions)
	if err != nil {
		return fmt.Errorf("building schema diff between migrated database and new schema: %w", err)
	}

	if len(toTargetSchemaStmts) > 0 {
		var stmtsStrs []string
		for _, stmt := range toTargetSchemaStmts {
			stmtsStrs = append(stmtsStrs, stmt.DDL)
		}
		return fmt.Errorf("validating plan failed. diff detected:\n%s", strings.Join(stmtsStrs, "\n"))
	}

	return nil
}

// executeStatementsIgnoreTimeouts executes the statements using the sql connection but ignores any provided timeouts.
// This function is currently used to validate migration plans.
func executeStatementsIgnoreTimeouts(ctx context.Context, connPool *sql.DB, statements []Statement, disableCheckFunctionBodies bool) error {
	conn, err := connPool.Conn(ctx)
	if err != nil {
		return fmt.Errorf("getting connection from pool: %w", err)
	}
	defer conn.Close()

	// Set a session-level statement_timeout to bound the execution of the migration plan.
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("SET SESSION statement_timeout = %d", (10*time.Second).Milliseconds())); err != nil {
		return fmt.Errorf("setting statement timeout: %w", err)
	}
	// PostgreSQL validates a routine's body at CREATE time (the check_function_bodies session
	// setting) unless it is turned off. Set it explicitly to the caller's choice rather than only
	// turning it off: the connection comes from a pool and keeps a previous caller's setting, so a
	// step that reconstructed a schema with the check off would otherwise leak that into the plan
	// apply on the same connection.
	checkFunctionBodies := "on"
	if disableCheckFunctionBodies {
		checkFunctionBodies = "off"
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("SET SESSION check_function_bodies = %s", checkFunctionBodies)); err != nil {
		return fmt.Errorf("setting check_function_bodies: %w", err)
	}
	// Due to the way *sql.Db works, when a statement_timeout is set for the session, it will NOT reset
	// by default when it's returned to the pool.
	//
	// We can't set the timeout at the TRANSACTION-level (for each transaction) because `ADD INDEX CONCURRENTLY`
	// must be executed within its own transaction block. Postgres will error if you try to set a TRANSACTION-level
	// timeout for it. SESSION-level statement_timeouts are respected by `ADD INDEX CONCURRENTLY`
	for _, stmt := range statements {
		if stmt.SkipValidation {
			// Skip statements that cannot be validated in temp DB (e.g., GRANT/REVOKE which reference roles
			// that don't exist in the temp DB)
			continue
		}
		if _, err := conn.ExecContext(ctx, stmt.ToSQL()); err != nil {
			return fmt.Errorf("executing migration statement: %s: %w", stmt.DDL, err)
		}
	}
	return nil
}
