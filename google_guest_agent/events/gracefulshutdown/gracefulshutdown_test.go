// Copyright 2024 Google LLC

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at

//     https://www.apache.org/licenses/LICENSE-2.0

// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package gracefulshutdown

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/GoogleCloudPlatform/guest-agent/metadata"
)

type mockMDSClient struct {
	keyVal string
	keyErr error
}

func (m *mockMDSClient) Get(ctx context.Context) (*metadata.Descriptor, error) {
	return nil, nil
}

func (m *mockMDSClient) GetKey(ctx context.Context, key string, headers map[string]string) (string, error) {
	return "", fmt.Errorf("GetKey() not yet implemented")
}

func (m *mockMDSClient) GetKeyRecursive(ctx context.Context, key string) (string, error) {
	return "", nil
}

func (m *mockMDSClient) Watch(ctx context.Context) (*metadata.Descriptor, error) {
	return nil, nil
}

func (m *mockMDSClient) WatchKey(ctx context.Context, key string) (string, error) {
	if key != "instance/shutdown-details/stop-state" {
		return "", fmt.Errorf("unexpected key: %s", key)
	}
	return m.keyVal, m.keyErr
}

func (m *mockMDSClient) WriteGuestAttributes(ctx context.Context, key string, value string) error {
	return nil
}

// mockStatusError simulates the metadata.MDSReqError behavior for testing.
type mockStatusError struct {
	status int
}

func (m *mockStatusError) Error() string { return "error" }
func (m *mockStatusError) Status() int   { return m.status }

// Is allows errors.As to match this against metadata.MDSReqError if needed,
// but since we are mocking the *client*, the client returns *this* error directly.
// However, the *real* code expects *metadata.MDSReqError.
// We cannot easily implement a struct that passes errors.As(..., &metadata.MDSReqError)
// unless it IS a metadata.MDSReqError.
// So we must construct a real metadata.MDSReqError in the test.
// Since we cannot set private fields of MDSReqError if they are in another package,
// we rely on the fact that we can create a pointer to it?
// Wait, MDSReqError is exported, but its fields `status` and `err` are private!
// I added `Status()` method, but I cannot CONSTRUCT one with a specific status from this package
// if I cannot write to `status` field.
// Let's check metadata/metadata.go again.
// type MDSReqError struct { status int; err error }
// Yes, fields are private. I cannot construct it in the test package.
//
// SOLUTION: I need to add a constructor or helper in `metadata` package to create this error for testing,
// OR I need to use an interface for the error check.
// Since I already modified `metadata.go`, I should check if I can add a constructor.
// OR I can use `reflect` (nasty) or `unsafe`.
// OR better: The `retry` package wraps the error.
// If I mock `WatchKey` to return a `fmt.Errorf("... %w", &metadata.MDSReqError{status: 404})`, it would work IF I could construct it.
//
// I will modify `metadata/metadata.go` to add `NewMDSReqError(status int, err error) *MDSReqError`
// This is useful for tests anyway.

func TestWatcherAPI(t *testing.T) {
	w := New()
	if w.ID() != WatcherID {
		t.Errorf("ID() = %q, want %q", w.ID(), WatcherID)
	}
	events := w.Events()
	if len(events) != 1 || events[0] != RunScriptEvent {
		t.Errorf("Events() = %v, want [%s]", events, RunScriptEvent)
	}
}

func TestRun_PendingStop(t *testing.T) {
	scriptRun := false
	originalRunScript := runGracefulShutdownScript
	defer func() { runGracefulShutdownScript = originalRunScript }()
	runGracefulShutdownScript = func() {
		scriptRun = true
	}

	client := &mockMDSClient{keyVal: "PENDING_STOP"}
	w := &Watcher{client: client}

	ctx := context.Background()
	renew, _, err := w.Run(ctx, RunScriptEvent)
	if err != nil {
		t.Errorf("Run() returned error: %v", err)
	}

	if renew {
		t.Errorf("Run() returned renew=true, want false for PENDING_STOP")
	}

	if !scriptRun {
		t.Error("graceful shutdown script was not run")
	}
}

func TestRun_NotPending(t *testing.T) {
	scriptRun := false
	originalRunScript := runGracefulShutdownScript
	defer func() { runGracefulShutdownScript = originalRunScript }()
	runGracefulShutdownScript = func() {
		scriptRun = true
	}

	client := &mockMDSClient{keyVal: "NONE"}
	w := &Watcher{client: client}

	ctx := context.Background()
	renew, _, err := w.Run(ctx, RunScriptEvent)
	if err != nil {
		t.Errorf("Run() returned error: %v", err)
	}

	if !renew {
		t.Error("Run() returned renew=false, want true for non-PENDING_STOP")
	}

	if scriptRun {
		t.Error("graceful shutdown script should not have run")
	}
}

func TestRun_404(t *testing.T) {
	// To test 404, we need to construct a real MDSReqError.
	// We will assume `metadata.NewMDSReqError` exists (I will add it).
	err404 := metadata.NewMDSReqError(404, fmt.Errorf("not found"))
	
	client := &mockMDSClient{keyErr: err404}
	w := &Watcher{client: client}

	// We use a context that cancels quickly to break the 1-minute wait.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	renew, _, err := w.Run(ctx, RunScriptEvent)
	
	// Expect error to be context cancelled, because we waited.
	if err != context.Canceled {
		t.Errorf("Run() returned error: %v, want context.Canceled (implying it waited)", err)
	}
	
	// Renew should be false because context cancelled (it exits).
	if renew {
		t.Error("Run() returned renew=true, want false on context cancel")
	}
}
