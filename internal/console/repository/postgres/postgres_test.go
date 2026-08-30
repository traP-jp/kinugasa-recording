package postgres

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	testPool       *pgxpool.Pool
	testPostgres   *exec.Cmd
	testTempDir    string
	testSetupError error
)

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	testPool, testPostgres, testTempDir, testSetupError = startTestPostgres(ctx)
	cancel()
	code := m.Run()
	if testPool != nil {
		testPool.Close()
	}
	if testPostgres != nil && testPostgres.Process != nil {
		_ = testPostgres.Process.Signal(os.Interrupt)
		_ = testPostgres.Wait()
	}
	if testTempDir != "" {
		_ = os.RemoveAll(testTempDir)
	}
	os.Exit(code)
}

func requireTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testSetupError != nil {
		t.Skipf("temporary PostgreSQL unavailable: %v", testSetupError)
	}
	return testPool
}

func resetDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := requireTestPool(t)
	_, err := pool.Exec(context.Background(), `
		TRUNCATE video_files, recording_cameras, takes,
		         camera_connections, camera_identities, sessions CASCADE`)
	if err != nil {
		t.Fatalf("truncate database: %v", err)
	}
	return pool
}

func startTestPostgres(ctx context.Context) (*pgxpool.Pool, *exec.Cmd, string, error) {
	if _, err := exec.LookPath("initdb"); err != nil {
		return nil, nil, "", err
	}
	tempDir, err := os.MkdirTemp("", "kinugasa-postgres-")
	if err != nil {
		return nil, nil, "", fmt.Errorf("make postgres temp directory: %w", err)
	}
	dataDir := filepath.Join(tempDir, "data")
	socketDir := filepath.Join(tempDir, "socket")
	if err := os.Mkdir(socketDir, 0o700); err != nil {
		return nil, nil, tempDir, fmt.Errorf("make postgres socket directory: %w", err)
	}

	initCommand := exec.CommandContext(ctx, "initdb",
		"-D", dataDir,
		"--auth=trust",
		"--username=postgres",
		"--no-locale",
		"--encoding=UTF8",
	)
	if output, err := initCommand.CombinedOutput(); err != nil {
		return nil, nil, tempDir, fmt.Errorf("initdb: %w: %s", err, output)
	}

	var postgresLog bytes.Buffer
	postgres := exec.Command(
		"postgres",
		"-D", dataDir,
		"-F",
		"-k", socketDir,
		"-c", "listen_addresses=",
	)
	postgres.Stdout = &postgresLog
	postgres.Stderr = &postgresLog
	if err := postgres.Start(); err != nil {
		return nil, nil, tempDir, fmt.Errorf("start postgres: %w", err)
	}

	connectionString := fmt.Sprintf("host=%s user=postgres dbname=postgres sslmode=disable", socketDir)
	var pool *pgxpool.Pool
	for ctx.Err() == nil {
		pool, err = pgxpool.New(ctx, connectionString)
		if err == nil {
			err = pool.Ping(ctx)
		}
		if err == nil {
			break
		}
		if pool != nil {
			pool.Close()
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		_ = postgres.Process.Signal(os.Interrupt)
		_ = postgres.Wait()
		return nil, nil, tempDir, fmt.Errorf("connect to postgres: %w: %s", err, postgresLog.String())
	}
	if err := Migrate(ctx, pool); err != nil {
		pool.Close()
		_ = postgres.Process.Signal(os.Interrupt)
		_ = postgres.Wait()
		return nil, nil, tempDir, fmt.Errorf("migrate test database: %w: %s", err, postgresLog.String())
	}
	return pool, postgres, tempDir, nil
}
