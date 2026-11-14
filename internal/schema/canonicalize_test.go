package schema

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"sync"
	"testing"

	_ "github.com/jackc/pgx/v4/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stripe/pg-schema-diff/internal/pgengine"
	"github.com/stripe/pg-schema-diff/internal/queries"
)

func TestGetSchema_RestoresCheckFunctionBodiesOnPooledConnection(t *testing.T) {
	engine, err := pgengine.StartEngine()
	require.NoError(t, err)
	defer engine.Close()

	db, err := engine.CreateDatabase()
	require.NoError(t, err)
	defer func() {
		require.NoError(t, db.DropDB())
	}()

	pool, err := sql.Open("pgx", db.GetDSN())
	require.NoError(t, err)
	defer pool.Close()
	// One connection makes the pool's only backend the one canonicalization uses, so its session
	// state can be read back after GetSchema returns it.
	pool.SetMaxOpenConns(1)

	_, err = pool.Exec(`CREATE FUNCTION public.one() RETURNS integer LANGUAGE sql AS $$ SELECT 1 $$;`)
	require.NoError(t, err)

	_, err = GetSchema(context.Background(), pool)
	require.NoError(t, err)

	conn, err := pool.Conn(context.Background())
	require.NoError(t, err)
	defer conn.Close()
	var setting string
	require.NoError(t, conn.QueryRowContext(context.Background(), "SHOW check_function_bodies").Scan(&setting))
	assert.Equal(t, "on", setting)
}

// poolStatsDB satisfies queries.DBTX and reports a pool cap; the scheduler only
// reads Stats, and the work functions in these tests do not use the connection.
type poolStatsDB struct {
	queries.DBTX
	maxOpen int
}

func (d poolStatsDB) Stats() sql.DBStats {
	return sql.DBStats{MaxOpenConnections: d.maxOpen}
}

// singleConnectionDB satisfies queries.DBTX without a pool: GetSchema's callers
// pass a single connection for those and the scheduler must stay serial.
type singleConnectionDB struct {
	queries.DBTX
}

func claimedIndices(t *testing.T, fetcher *schemaFetcher, objects int) []int {
	t.Helper()
	var mu sync.Mutex
	seen := make([]int, 0, objects)
	err := fetcher.canonicalizeOverSessions(context.Background(), objects, func(_ context.Context, claim func() (int, bool)) error {
		for {
			i, ok := claim()
			if !ok {
				return nil
			}
			mu.Lock()
			seen = append(seen, i)
			mu.Unlock()
		}
	})
	require.NoError(t, err)
	sort.Ints(seen)
	return seen
}

func TestCanonicalizeOverSessions_ClaimsEachObjectOnce(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fetcher *schemaFetcher
		objects int
	}{
		{name: "no objects", fetcher: &schemaFetcher{db: poolStatsDB{maxOpen: 4}}, objects: 0},
		{name: "one object", fetcher: &schemaFetcher{db: poolStatsDB{maxOpen: 4}}, objects: 1},
		{name: "more objects than sessions", fetcher: &schemaFetcher{db: poolStatsDB{maxOpen: 4}}, objects: 100},
		{name: "pool cap of one", fetcher: &schemaFetcher{db: poolStatsDB{maxOpen: 1}}, objects: 20},
		{name: "single connection", fetcher: &schemaFetcher{db: singleConnectionDB{}}, objects: 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := claimedIndices(t, tc.fetcher, tc.objects)
			require.Len(t, seen, tc.objects)
			for i, index := range seen {
				assert.Equal(t, i, index)
			}
		})
	}
}

func TestCanonicalizeOverSessions_ErrorSurfaces(t *testing.T) {
	expectedErr := errors.New("some error")
	fetcher := &schemaFetcher{db: poolStatsDB{maxOpen: 4}}

	err := fetcher.canonicalizeOverSessions(context.Background(), 100, func(_ context.Context, claim func() (int, bool)) error {
		if _, ok := claim(); !ok {
			return errors.New("expected an object to claim")
		}
		return expectedErr
	})

	require.Error(t, err)
	// A worker that loses the race to the failure may report the cancellation
	// instead; either way the failure must reach the caller.
	assert.True(t, errors.Is(err, expectedErr) || errors.Is(err, context.Canceled), "unexpected error: %v", err)
}
