# Guest Agent Script Execution Architecture

This document details how the Google Guest Agent triggers and executes startup, shutdown, and the newly implemented graceful shutdown scripts.

## 1. High-Level Overview
The Guest Agent ecosystem uses a combination of **Systemd Services**, a **Main Agent Process** (Go), and a **Metadata Script Runner** (Go) to manage scripts defined in GCE Metadata.

---

## 2. Standard Startup/Shutdown Scripts
These are triggered by the OS state transitions (Boot/Halt).

### Systemd Services
*   **`google-startup-scripts.service`**: Runs on boot (`Type=oneshot`). Executes `/usr/bin/google_metadata_script_runner_adapt startup`.
*   **`google-shutdown-scripts.service`**: Runs on shutdown. It stays active during the session and runs `/usr/bin/google_metadata_script_runner_adapt shutdown` as its `ExecStop` action.

---

## 3. Graceful Shutdown Implementation
Graceful shutdown is triggered by an **API signal** from GCE (e.g., during Preemptible VM reclamation or Spot Instance termination) *before* the OS-level shutdown begins.

### The Watcher Mechanism
The main `google-guest-agent` process monitors the metadata server for state changes.

1.  **New Watcher**: `google_guest_agent/events/gracefulshutdown/`.
2.  **Long Polling**:
    *   The watcher uses a hanging GET request on `http://metadata.google.internal/computeMetadata/v1/instance/shutdown-details/stop-state`.
    *   **Wait for Change**: It uses the query parameter `wait_for_change=true`.
    *   **Gotcha (Key Name)**: The key is `stop-state` (hyphenated), not camelCase.
3.  **Event Flow**:
    *   **State = 404 (Not Found)**: Handled as a silent wait for 60 seconds. This occurs on VMs where the feature is not active or exposed.
    *   **State = UNSPECIFIED**: The normal idle state. The watcher continues to hang/poll using the `last_etag`.
    *   **State = PENDING_STOP**: The watcher detects this value and triggers the execution.

### Execution Workflow
1.  **Signal Detected**: The watcher runs `systemctl start google-graceful-shutdown-scripts.service`.
2.  **Graceful Service**: A new `Type=oneshot` service calls `/usr/bin/google_metadata_script_runner_adapt graceful-shutdown`.

---

## 4. Technical Implementation Details

### Metadata Client Updates (`metadata/metadata.go`)
*   **`WatchKey(ctx, key)`**: New method specifically for watching a single key with long-polling. It manages the `last_etag` per-instance to avoid conflicts with the main metadata watcher.
*   **`MDSReqError`**: Includes a `Status()` method and `NewMDSReqError` constructor for robust error inspection via `errors.As`.

### Packaging and Presets
*   **`90-google-guest-agent.preset`**: Enables `google-graceful-shutdown-scripts.service` by default.

---

## 5. Critical "Gotchas" for Future Maintenance

### 1. Header vs Query Param
`wait_for_change` and `last_etag` **must** be URL Query Parameters. The MDS ignores them if sent as HTTP Headers.

### 2. The 404/UNSPECIFIED Duality
Depending on the environment, the `stop-state` key may return a 404 or a 200 with `UNSPECIFIED`. Both must be handled without flooding logs.

### 3. Windows Compatibility
On Windows, the watcher executes `GCEMetadataScriptRunner.exe graceful-shutdown` directly using an absolute path resolved from the agent's own executable location.
