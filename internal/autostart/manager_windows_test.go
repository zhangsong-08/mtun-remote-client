// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

//go:build windows

package autostart

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestEncodePowerShellCommandUTF16LE(t *testing.T) {
	const script = "$name = 'mtun-服务端'"
	data, err := base64.StdEncoding.DecodeString(encodePowerShellCommand(script))
	if err != nil {
		t.Fatal(err)
	}
	if len(data)%2 != 0 {
		t.Fatalf("encoded byte length %d is odd", len(data))
	}
	values := make([]uint16, len(data)/2)
	for i := range values {
		values[i] = binary.LittleEndian.Uint16(data[i*2:])
	}
	if got := string(utf16.Decode(values)); got != script {
		t.Fatalf("decoded script = %q, want %q", got, script)
	}
}

func TestInstallRoot(t *testing.T) {
	tests := []struct {
		config string
		want   string
	}{
		{`C:\Program Files\mtun\configs\server.yaml`, `C:\Program Files\mtun`},
		{`C:\Users\me\mtun\Configs\client.yaml`, `C:\Users\me\mtun`},
		{`C:\Users\me\mtun\server.yaml`, `C:\Users\me\mtun`},
	}
	for _, test := range tests {
		if got := installRoot(test.config); got != test.want {
			t.Errorf("installRoot(%q) = %q, want %q", test.config, got, test.want)
		}
	}
}

func TestWrapperScriptPathsAndBOM(t *testing.T) {
	root := `C:\Users\O'Neil\mtun`
	config := filepath.Join(root, "configs", "server.yaml")
	exe := filepath.Join(root, "bin", "mtun.exe")
	data := wrapperScript("server", config, exe, root)
	if len(data) < 3 || string(data[:3]) != "\xef\xbb\xbf" {
		t.Fatal("wrapper does not start with a UTF-8 BOM")
	}
	script := string(data[3:])
	if strings.Contains(script, "$ErrorActionPreference = 'Stop'") {
		t.Fatal("wrapper must not turn native stderr log output into a terminating PowerShell error")
	}
	for _, want := range []string{
		`& 'C:\Users\O''Neil\mtun\bin\mtun.exe' 'server' '-c' 'C:\Users\O''Neil\mtun\configs\server.yaml'`,
		`1>> 'C:\Users\O''Neil\mtun\logs\server.log'`,
		`2>> 'C:\Users\O''Neil\mtun\logs\server.err'`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("wrapper does not contain %q:\n%s", want, script)
		}
	}
}

func TestScheduledTaskStatusUsesEnabledSetting(t *testing.T) {
	if !strings.Contains(statusScript, "$taskEnabled = [bool]$task.Settings.Enabled") {
		t.Fatal("running tasks must report their enabled state from Settings.Enabled, not State")
	}
}

func TestInspectionJSONAndStatus(t *testing.T) {
	var in inspection
	data := `{"service_exists":true,"service_start_mode":"Auto","task_exists":true,"task_enabled":true,"task_trigger":"AtStartup","startup_link":false}`
	if err := json.Unmarshal([]byte(data), &in); err != nil {
		t.Fatal(err)
	}
	status := statusFromInspection(in)
	if !status.Supported || !status.Enabled || status.Mode != "nssm" || status.Trigger != "AtStartup" || !status.Conflict {
		t.Fatalf("unexpected NSSM-priority status: %+v", status)
	}

	status = statusFromInspection(inspection{TaskExists: true, TaskEnabled: true, TaskTrigger: "AtLogOn"})
	if !status.Enabled || status.Mode != "scheduled_task" || status.Trigger != "AtLogOn" || status.Conflict {
		t.Fatalf("unexpected scheduled task status: %+v", status)
	}

	status = statusFromInspection(inspection{ServiceExists: true, ServiceStartMode: "Disabled", TaskExists: true})
	if status.Enabled || status.Conflict {
		t.Fatalf("disabled entries should not conflict: %+v", status)
	}

	status = statusFromInspection(inspection{StartupLink: true})
	if !status.Enabled || status.Mode != "startup_link" || status.Trigger != "AtLogOn" {
		t.Fatalf("unexpected startup link status: %+v", status)
	}
}

func TestStatusCommandUsesEncodedPowerShellAndEnvironment(t *testing.T) {
	original := executeCommand
	defer func() { executeCommand = original }()

	manager, err := New("client", `C:\opt\mtun\configs\client.yaml`)
	if err != nil {
		t.Fatal(err)
	}
	var gotName string
	var gotArgs, gotEnv []string
	executeCommand = func(_ context.Context, name string, args []string, env []string) (commandResult, error) {
		gotName = name
		gotArgs = append([]string(nil), args...)
		gotEnv = append([]string(nil), env...)
		return commandResult{stdout: `{"task_exists":true,"task_enabled":true,"task_trigger":"AtLogOn"}`}, nil
	}
	status, err := manager.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Mode != "scheduled_task" || !status.Enabled {
		t.Fatalf("unexpected status: %+v", status)
	}
	if gotName != "powershell.exe" {
		t.Fatalf("command = %q", gotName)
	}
	encoded := ""
	for i, arg := range gotArgs {
		if arg == "-EncodedCommand" && i+1 < len(gotArgs) {
			encoded = gotArgs[i+1]
		}
	}
	if encoded == "" || encoded != encodePowerShellCommand(statusScript) {
		t.Fatal("PowerShell script was not passed with -EncodedCommand")
	}
	wantArgs := []string{
		"-NoLogo", "-NoProfile", "-NonInteractive",
		"-ExecutionPolicy", "Bypass", "-EncodedCommand", encodePowerShellCommand(statusScript),
	}
	if strings.Join(gotArgs, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("PowerShell args = %q, want %q", gotArgs, wantArgs)
	}
	joinedEnv := strings.Join(gotEnv, "\n")
	for _, want := range []string{
		"MTUN_AUTOSTART_NAME=mtun-client",
		"MTUN_AUTOSTART_ROLE=client",
		`MTUN_AUTOSTART_CONFIG=C:\opt\mtun\configs\client.yaml`,
	} {
		if !strings.Contains(joinedEnv, want) {
			t.Errorf("environment does not contain %q: %s", want, joinedEnv)
		}
	}
}

func TestClassifyCommandErrors(t *testing.T) {
	permission := classifyCommandError(commandResult{stderr: "Access is denied"}, errors.New("exit 1"))
	if !errors.Is(permission, ErrPermission) {
		t.Fatalf("permission error = %v", permission)
	}
	conflict := classifyCommandError(commandResult{stderr: "configuration conflict"}, errors.New("exit 1"))
	if !errors.Is(conflict, ErrConflict) {
		t.Fatalf("conflict error = %v", conflict)
	}
}
