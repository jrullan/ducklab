package daemon

import (
	"errors"
	"os"
	"testing"
	"time"
)

// B-530 review (Codex): two engines starting at once each saw no
// engine.json, each wrote its own human.token, then raced to engine.json —
// reproduced as engine.json=agent-b beside human.token=human-a, and the
// desktop lost every person-only door. Both starts run here exactly as the
// engine runs them (lock, then publish); exactly one may publish, and the
// published pair must be one engine's.
func TestTwoSimultaneousStartsPublishOneCredentialPair(t *testing.T) {
	isolateDaemonState(t)
	for round := 0; round < 20; round++ {
		_ = os.Remove(mustPath(t, EngineJSONPath))
		_ = DeleteHumanToken()
		start := make(chan struct{})
		type result struct {
			name string
			lock *EngineLock
			err  error
		}
		results := make(chan result, 2)
		for _, name := range []string{"a", "b"} {
			go func(name string) {
				<-start
				lock, err := AcquireEngineLock(100 * time.Millisecond)
				if err == nil {
					// The winner's pid is this test's: alive, as a serving
					// engine's would be.
					err = WriteEngineFiles(&EngineInfo{PID: os.Getpid(), Port: 1, Token: "agent-" + name}, "human-"+name)
				}
				results <- result{name, lock, err}
			}(name)
		}
		close(start)
		var won []string
		var locks []*EngineLock
		for i := 0; i < 2; i++ {
			r := <-results
			if r.lock != nil {
				locks = append(locks, r.lock)
			}
			if r.err == nil {
				won = append(won, r.name)
			}
		}
		if len(won) != 1 {
			t.Fatalf("round %d: %d starts published (%v), want exactly one", round, len(won), won)
		}
		info, err := ReadEngineJSON()
		if err != nil {
			t.Fatal(err)
		}
		human, err := ReadHumanToken()
		if err != nil {
			t.Fatal(err)
		}
		if info.Token != "agent-"+won[0] || human != "human-"+won[0] {
			t.Fatalf("round %d: engine.json=%s beside human.token=%s; want one engine's pair (%s)", round, info.Token, human, won[0])
		}
		if len(locks) != 1 {
			t.Fatalf("round %d: %d starts hold the engine lock, want one", round, len(locks))
		}
		for _, l := range locks {
			l.Release()
		}
	}
}

// A dead engine's lock must never block a fresh start: the lock file a crash
// leaves behind means nothing, and a holder that dies releases the lock.
func TestADeadEnginesLockDoesNotBlockAStart(t *testing.T) {
	isolateDaemonState(t)
	held, err := AcquireEngineLock(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireEngineLock(0); !errors.Is(err, ErrEngineLocked) {
		t.Fatalf("second acquire while held = %v, want ErrEngineLocked", err)
	}
	// The holder goes away (a crash closes its descriptors the same way);
	// the file stays behind.
	held.Release()
	if _, err := os.Stat(mustPath(t, LockPath)); err != nil {
		t.Fatalf("lock file should remain on disk: %v", err)
	}
	next, err := AcquireEngineLock(0)
	if err != nil {
		t.Fatalf("a start after the holder died = %v, want the lock", err)
	}
	next.Release()
}

// A restart starts the new engine as soon as the old stops answering; the
// old one may still be running its cleanup. The new start waits for it.
func TestAStartWaitsForAnEngineOnItsWayOut(t *testing.T) {
	isolateDaemonState(t)
	old, err := AcquireEngineLock(0)
	if err != nil {
		t.Fatal(err)
	}
	go func() { time.Sleep(150 * time.Millisecond); old.Release() }()
	next, err := AcquireEngineLock(5 * time.Second)
	if err != nil {
		t.Fatalf("start during the old engine's exit = %v, want the lock once it is released", err)
	}
	next.Release()
}

func mustPath(t *testing.T, f func() (string, error)) string {
	t.Helper()
	p, err := f()
	if err != nil {
		t.Fatal(err)
	}
	return p
}
