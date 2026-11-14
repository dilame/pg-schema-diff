package schema

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v4/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stripe/pg-schema-diff/internal/pgengine"
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
		name     string
		sessions int
		objects  int
	}{
		{name: "no objects", sessions: 5, objects: 0},
		{name: "one object", sessions: 5, objects: 1},
		{name: "more objects than sessions", sessions: 5, objects: 100},
		{name: "pool cap of one", sessions: 1, objects: 20},
		{name: "single connection", sessions: 0, objects: 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := claimedIndices(t, &schemaFetcher{canonicalizerSessions: tc.sessions}, tc.objects)
			require.Len(t, seen, tc.objects)
			for i, index := range seen {
				assert.Equal(t, i, index)
			}
		})
	}
}

// TestCanonicalizeOverSessions_Concurrency observes how many sessions the
// scheduler starts at once: each work run signals its entry and blocks until
// the test has seen the expected number and checked that no further session
// starts.
func TestCanonicalizeOverSessions_Concurrency(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sessions int
		objects  int
		want     int
	}{
		{name: "pool cap at least five", sessions: 5, objects: 100, want: 5},
		{name: "pool cap three", sessions: 3, objects: 100, want: 3},
		{name: "pool cap one", sessions: 1, objects: 100, want: 1},
		{name: "single connection", sessions: 0, objects: 100, want: 1},
		{name: "fewer objects than sessions", sessions: 5, objects: 3, want: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fetcher := &schemaFetcher{canonicalizerSessions: tc.sessions}
			entered := make(chan struct{}, tc.want+1)
			release := make(chan struct{})
			done := make(chan error, 1)

			go func() {
				done <- fetcher.canonicalizeOverSessions(context.Background(), tc.objects, func(_ context.Context, _ func() (int, bool)) error {
					entered <- struct{}{}
					<-release
					return nil
				})
			}()

			for i := 0; i < tc.want; i++ {
				select {
				case <-entered:
				case <-time.After(2 * time.Second):
					t.Fatalf("only %d of %d expected sessions started", i, tc.want)
				}
			}
			select {
			case <-entered:
				t.Fatalf("more than %d sessions started", tc.want)
			case <-time.After(50 * time.Millisecond):
			}

			close(release)
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(2 * time.Second):
				t.Fatal("canonicalization did not finish")
			}
		})
	}
}

func TestCanonicalizeOverSessions_ErrorSurfaces(t *testing.T) {
	expectedErr := errors.New("some error")
	fetcher := &schemaFetcher{canonicalizerSessions: 5}

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
