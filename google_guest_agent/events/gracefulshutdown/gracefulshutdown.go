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

// Package gracefulshutdown implements the graceful shutdown events watcher.
package gracefulshutdown

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/GoogleCloudPlatform/guest-agent/metadata"
	"github.com/GoogleCloudPlatform/guest-logging-go/logger"
)

const (
	// WatcherID is the graceful shutdown watcher's ID.
	WatcherID = "graceful-shutdown-watcher"
	// RunScriptEvent is the graceful shutdown's event type ID.
	RunScriptEvent = "graceful-shutdown-watcher,run-script"
)

var (
	runGracefulShutdownScript = func() {
		logger.Infof("Starting graceful shutdown scripts.")
		if runtime.GOOS == "linux" {
			cmd := exec.Command("systemctl", "start", "google-graceful-shutdown-scripts.service")
			if err := cmd.Run(); err != nil {
				logger.Errorf("failed to run graceful shutdown script: %v", err)
			}
		} else if runtime.GOOS == "windows" {
			// On Windows, we run the script runner directly.
			// We assume GCEMetadataScriptRunner.exe is in the same directory as the agent.
			exePath, err := os.Executable()
			if err != nil {
				logger.Errorf("failed to get agent executable path: %v", err)
				return
			}
			runnerPath := filepath.Join(filepath.Dir(exePath), "GCEMetadataScriptRunner.exe")
			cmd := exec.Command(runnerPath, "graceful-shutdown")
			if err := cmd.Run(); err != nil {
				logger.Errorf("failed to run graceful shutdown script: %v", err)
			}
		}
	}
)

// Watcher is the graceful shutdown event watcher implementation.
type Watcher struct {
	client metadata.MDSClientInterface
}

// New allocates and initializes a new Watcher.
func New() *Watcher {
	return &Watcher{
		client: metadata.New(),
	}
}

// ID returns the graceful shutdown event watcher id.
func (mp *Watcher) ID() string {
	return WatcherID
}

// Events returns an slice with all implemented events.
func (mp *Watcher) Events() []string {
	return []string{RunScriptEvent}
}

// Run listens to metadata changes and report back the event.
func (mp *Watcher) Run(ctx context.Context, evType string) (bool, interface{}, error) {
	val, err := mp.client.WatchKey(ctx, "instance/shutdown-details/stop-state")
	if err != nil {
		// If the key doesn't exist (404), it means graceful shutdown is not in progress.
		// We wait and renew the watcher silently.
		var mdsErr *metadata.MDSReqError
		if errors.As(err, &mdsErr) && mdsErr.Status() == 404 {
			select {
			case <-ctx.Done():
				return false, nil, ctx.Err()
			case <-time.After(time.Minute):
				return true, nil, nil
			}
		}
		// For other errors (network issues, 500s, etc.), we log an error and retry after a shorter delay.
		logger.Errorf("error watching graceful shutdown metadata: %v", err)
		select {
		case <-ctx.Done():
			return false, nil, ctx.Err()
		case <-time.After(5 * time.Second):
			return true, nil, nil
		}
	}

	if strings.TrimSpace(val) == "PENDING_STOP" {
		runGracefulShutdownScript()
		// VM is stopping, no need to renew the watcher.
		return false, nil, nil
	}

	// If the state is something else (e.g. "NONE" or empty), keep watching.
	return true, nil, nil
}
