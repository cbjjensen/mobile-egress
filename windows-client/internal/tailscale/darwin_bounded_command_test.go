//go:build darwin

package tailscale

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestDarwinBoundedCommandKillsDescendantPipeHolderOnCancellation(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "descendant.pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	started := time.Now()
	go func() {
		_, err := runDarwinBoundedCommand(
			ctx,
			darwinDescendantCommandFactory("parent-timeout", pidFile, 0),
			"/usr/bin/codesign",
			[]string{"--verify", "fixture"},
			[]string{"LC_ALL=C", "LANG=C", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"},
			128,
		)
		result <- err
	}()
	descendantPID := awaitDarwinTestPID(t, pidFile, 2*time.Second)
	cancel()
	err := awaitDarwinCommandResult(t, result)
	if err == nil {
		t.Fatal("canceled command returned nil error")
	}
	if elapsed := time.Since(started); elapsed > maximumDarwinBoundedCommandDuration() {
		t.Fatalf("canceled command returned after %v", elapsed)
	}
	assertDarwinTestProcessGone(t, descendantPID)
}

func TestDarwinBoundedCommandKillsDescendantPipeHolderOnOverflow(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "descendant.pid")
	const limit = 128
	started := time.Now()
	output, err := runDarwinBoundedCommand(
		context.Background(),
		darwinDescendantCommandFactory("parent-overflow", pidFile, limit+1),
		"/usr/bin/codesign",
		[]string{"--display", "fixture"},
		[]string{"LC_ALL=C", "LANG=C", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"},
		limit,
	)
	if !errors.Is(err, errDarwinBoundedCommandOutputLimit) || output != nil {
		t.Fatalf("overflow output len=%d error=%v", len(output), err)
	}
	if elapsed := time.Since(started); elapsed > maximumDarwinBoundedCommandDuration() {
		t.Fatalf("overflow command returned after %v", elapsed)
	}
	descendantPID := awaitDarwinTestPID(t, pidFile, time.Second)
	assertDarwinTestProcessGone(t, descendantPID)
}

func TestDarwinBoundedCommandCleansPipeHolderAfterLeaderExit(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "descendant.pid")
	started := time.Now()
	output, err := runDarwinBoundedCommand(
		context.Background(),
		darwinDescendantCommandFactory("parent-exit", pidFile, 0),
		"/usr/bin/codesign",
		[]string{"--verify", "fixture"},
		[]string{"LC_ALL=C", "LANG=C", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"},
		128,
	)
	if err != nil || len(output) != 0 {
		t.Fatalf("leader-exit output len=%d error=%v", len(output), err)
	}
	if elapsed := time.Since(started); elapsed > maximumDarwinBoundedCommandDuration() {
		t.Fatalf("pipe-holder cleanup returned after %v", elapsed)
	}
	descendantPID := awaitDarwinTestPID(t, pidFile, time.Second)
	assertDarwinTestProcessGone(t, descendantPID)
}

func TestDarwinBoundedCommandCleansPipeHolderAfterNonzeroLeaderExit(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "descendant.pid")
	started := time.Now()
	output, err := runDarwinBoundedCommand(
		context.Background(),
		darwinDescendantCommandFactory("parent-nonzero", pidFile, 0),
		"/usr/bin/codesign",
		[]string{"--verify", "fixture"},
		[]string{"LC_ALL=C", "LANG=C", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"},
		128,
	)
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 33 || output != nil {
		t.Fatalf("nonzero leader output len=%d error=%v", len(output), err)
	}
	if elapsed := time.Since(started); elapsed > maximumDarwinBoundedCommandDuration() {
		t.Fatalf("nonzero pipe-holder cleanup returned after %v", elapsed)
	}
	descendantPID := awaitDarwinTestPID(t, pidFile, time.Second)
	assertDarwinTestProcessGone(t, descendantPID)
}

func TestDarwinBoundedCommandClosesLateKqueueAttachRace(t *testing.T) {
	for _, test := range []struct {
		name     string
		mode     string
		exitCode int
	}{
		{name: "zero leader", mode: "parent-exit"},
		{name: "nonzero leader", mode: "parent-nonzero", exitCode: 33},
	} {
		t.Run(test.name, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "descendant.pid")
			system := &darwinZombieBeforeRegisterSystem{}
			output, err := runDarwinBoundedCommandWithSystem(
				context.Background(),
				darwinDescendantCommandFactory(test.mode, pidFile, 0),
				"/usr/bin/codesign",
				[]string{"--verify", "fixture"},
				[]string{"LC_ALL=C", "LANG=C", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"},
				128,
				system,
			)
			if test.exitCode == 0 {
				if err != nil || len(output) != 0 {
					t.Fatalf("late-attach zero leader output len=%d error=%v", len(output), err)
				}
			} else {
				var exitError *exec.ExitError
				if !errors.As(err, &exitError) || exitError.ExitCode() != test.exitCode || output != nil {
					t.Fatalf("late-attach nonzero leader output len=%d error=%v", len(output), err)
				}
			}
			if !system.sawZombieBeforeRegister {
				t.Fatal("test monitor did not observe a zombie before registering NOTE_EXIT")
			}
			descendantPID := awaitDarwinTestPID(t, pidFile, time.Second)
			assertDarwinTestProcessGone(t, descendantPID)
		})
	}
}

func TestDarwinBoundedCommandRejectsEscapedPipeHolder(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "escaped.pid")
	started := time.Now()
	output, err := runDarwinBoundedCommand(
		context.Background(),
		darwinDescendantCommandFactory("parent-escaped", pidFile, 0),
		"/usr/bin/codesign",
		[]string{"--verify", "fixture"},
		[]string{"LC_ALL=C", "LANG=C", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"},
		128,
	)
	if !errors.Is(err, errDarwinBoundedCommandProcessTree) || output != nil {
		t.Fatalf("escaped holder output len=%d error=%v", len(output), err)
	}
	if elapsed := time.Since(started); elapsed > maximumDarwinBoundedCommandDuration() {
		t.Fatalf("escaped pipe-holder rejection returned after %v", elapsed)
	}
	escapedPID := awaitDarwinTestPID(t, pidFile, time.Second)
	cleanupDarwinEscapedTestProcess(t, escapedPID)
}

func TestDarwinBoundedCommandSupervisorSignalsOnlyWhileLeaderIsUnreaped(t *testing.T) {
	for _, test := range []struct {
		name             string
		cancelContext    bool
		overflow         bool
		registerErr      error
		status           darwinBoundedCommandLeaderStatus
		statusErr        error
		polls            []darwinBoundedCommandTestPoll
		terminationClock bool
		wantEvents       []string
		wantLifecycleErr bool
	}{
		{
			name:       "natural exit",
			status:     darwinBoundedCommandLeaderLive,
			polls:      []darwinBoundedCommandTestPoll{{exited: true}},
			wantEvents: []string{"register", "status", "poll", "signal", "wait"},
		},
		{
			name:       "register succeeds after leader was already zombie",
			status:     darwinBoundedCommandLeaderZombie,
			wantEvents: []string{"register", "status", "signal", "wait"},
		},
		{
			name:          "parent cancellation",
			cancelContext: true,
			status:        darwinBoundedCommandLeaderLive,
			polls:         []darwinBoundedCommandTestPoll{{exited: true}},
			wantEvents:    []string{"register", "status", "signal", "poll", "signal", "wait"},
		},
		{
			name:       "output overflow",
			overflow:   true,
			status:     darwinBoundedCommandLeaderLive,
			polls:      []darwinBoundedCommandTestPoll{{exited: true}},
			wantEvents: []string{"register", "status", "signal", "poll", "signal", "wait"},
		},
		{
			name:             "registration failure",
			registerErr:      errors.New("registration failed"),
			status:           darwinBoundedCommandLeaderLive,
			wantEvents:       []string{"register", "status", "signal", "wait"},
			wantLifecycleErr: true,
		},
		{
			name:             "registration and status failure",
			registerErr:      errors.New("registration failed"),
			statusErr:        errors.New("status failed"),
			wantEvents:       []string{"register", "status", "signal", "wait"},
			wantLifecycleErr: true,
		},
		{
			name:        "registration loses exit race",
			registerErr: unix.ESRCH,
			status:      darwinBoundedCommandLeaderZombie,
			wantEvents:  []string{"register", "status", "signal", "wait"},
		},
		{
			name:             "monitor failure",
			status:           darwinBoundedCommandLeaderLive,
			polls:            []darwinBoundedCommandTestPoll{{err: errors.New("monitor failed")}},
			wantEvents:       []string{"register", "status", "poll", "signal", "wait"},
			wantLifecycleErr: true,
		},
		{
			name:             "leader status syscall uncertainty",
			statusErr:        errors.New("sysctl failed"),
			wantEvents:       []string{"register", "status", "signal", "wait"},
			wantLifecycleErr: true,
		},
		{
			name:             "leader status classification uncertainty",
			wantEvents:       []string{"register", "status", "signal", "wait"},
			wantLifecycleErr: true,
		},
		{
			name:             "termination deadline",
			cancelContext:    true,
			status:           darwinBoundedCommandLeaderLive,
			polls:            []darwinBoundedCommandTestPoll{{exited: false}},
			terminationClock: true,
			wantEvents:       []string{"register", "status", "signal", "poll", "signal", "kill", "wait"},
			wantLifecycleErr: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			events := []string{}
			reaped := false
			monitor := &darwinBoundedCommandTestMonitor{
				events: &events, registerErr: test.registerErr, status: test.status,
				statusErr: test.statusErr,
				polls:     append([]darwinBoundedCommandTestPoll(nil), test.polls...),
			}
			ctx := context.Background()
			if test.cancelContext {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			overflow := make(chan struct{})
			if test.overflow {
				close(overflow)
			}
			process := darwinBoundedCommandProcess{
				pid: 4107,
				wait: func() error {
					events = append(events, "wait")
					reaped = true
					return nil
				},
				kill: func() error {
					if reaped {
						t.Fatal("positive-PID kill occurred after reap")
					}
					events = append(events, "kill")
					return nil
				},
			}
			now := time.Now
			if test.terminationClock {
				base := time.Unix(100, 0)
				calls := 0
				now = func() time.Time {
					calls++
					if calls == 1 {
						return base
					}
					return base.Add(darwinBoundedCommandTreeExitDelay)
				}
			}
			_, lifecycleErr := superviseDarwinBoundedCommandWithClock(
				ctx,
				monitor,
				process,
				overflow,
				func(pid int) error {
					if reaped {
						t.Fatal("negative-PGID signal occurred after reap")
					}
					if pid != process.pid {
						t.Fatalf("signaled PID = %d, want %d", pid, process.pid)
					}
					events = append(events, "signal")
					return nil
				},
				now,
			)
			if (lifecycleErr != nil) != test.wantLifecycleErr ||
				(test.wantLifecycleErr && !errors.Is(lifecycleErr, errDarwinBoundedCommandProcessTree)) {
				t.Fatalf("lifecycle error = %v, want fixed=%t", lifecycleErr, test.wantLifecycleErr)
			}
			if fmt.Sprint(events) != fmt.Sprint(test.wantEvents) {
				t.Fatalf("events = %v, want %v", events, test.wantEvents)
			}
			if !reaped {
				t.Fatal("supervisor returned before the direct leader was reaped")
			}
		})
	}
}

func TestDarwinBoundedCommandKqueueCreationFailureDoesNotCreateCommand(t *testing.T) {
	factoryCalls := 0
	system := &darwinBoundedCommandTestSystem{openErr: errors.New("kqueue unavailable")}
	output, err := runDarwinBoundedCommandWithSystem(
		context.Background(),
		func(context.Context, string, ...string) *exec.Cmd {
			factoryCalls++
			return nil
		},
		"/usr/bin/codesign",
		[]string{"--verify", "fixture"},
		[]string{"LC_ALL=C", "LANG=C", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"},
		128,
		system,
	)
	if !errors.Is(err, errDarwinBoundedCommandProcessTree) || output != nil || factoryCalls != 0 {
		t.Fatalf("output=%v error=%v factory calls=%d", output, err, factoryCalls)
	}
}

func TestDarwinBoundedCommandExitRegistrationIsExact(t *testing.T) {
	registration := newDarwinBoundedCommandExitRegistration(4107)
	if registration.Ident != 4107 || registration.Filter != unix.EVFILT_PROC ||
		registration.Flags != unix.EV_ADD|unix.EV_ENABLE|unix.EV_ONESHOT ||
		registration.Fflags != unix.NOTE_EXIT || registration.Data != 0 || registration.Udata != nil {
		t.Fatalf("registration = %#v", registration)
	}
}

func TestDarwinBoundedCommandLeaderStatusClassificationIsExact(t *testing.T) {
	for _, test := range []struct {
		name        string
		expectedPID int
		observedPID int
		processStat int8
		want        darwinBoundedCommandLeaderStatus
		wantErr     bool
	}{
		{name: "idle is live", expectedPID: 4107, observedPID: 4107, processStat: 1, want: darwinBoundedCommandLeaderLive},
		{name: "running is live", expectedPID: 4107, observedPID: 4107, processStat: 2, want: darwinBoundedCommandLeaderLive},
		{name: "sleeping is live", expectedPID: 4107, observedPID: 4107, processStat: 3, want: darwinBoundedCommandLeaderLive},
		{name: "stopped is live", expectedPID: 4107, observedPID: 4107, processStat: 4, want: darwinBoundedCommandLeaderLive},
		{name: "zombie", expectedPID: 4107, observedPID: 4107, processStat: 5, want: darwinBoundedCommandLeaderZombie},
		{name: "different pid", expectedPID: 4107, observedPID: 4108, processStat: 2, wantErr: true},
		{name: "zero expected pid", observedPID: 4107, processStat: 2, wantErr: true},
		{name: "zero status", expectedPID: 4107, observedPID: 4107, wantErr: true},
		{name: "unknown status", expectedPID: 4107, observedPID: 4107, processStat: 6, wantErr: true},
		{name: "negative status", expectedPID: 4107, observedPID: 4107, processStat: -1, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := classifyDarwinBoundedCommandLeaderStatus(test.expectedPID, test.observedPID, test.processStat)
			if got != test.want || (err != nil) != test.wantErr || (test.wantErr && !errors.Is(err, errDarwinBoundedCommandProcessTree)) {
				t.Fatalf("status=%d error=%v, want %d fixed=%t", got, err, test.want, test.wantErr)
			}
		})
	}
}

func TestDarwinBoundedCommandResolvesOnlyVerifiedZombieGroupEPERM(t *testing.T) {
	member := func(pid, pgid int32, status int8) unix.KinfoProc {
		return unix.KinfoProc{
			Proc:  unix.ExternProc{P_pid: pid, P_stat: status},
			Eproc: unix.Eproc{Pgid: pgid},
		}
	}
	permissionErr := unix.EPERM
	inspectionErr := errors.New("process group inspection failed")
	for _, test := range []struct {
		name          string
		signalErr     error
		leader        *unix.KinfoProc
		leaderErr     error
		inspectionErr error
		members       []unix.KinfoProc
		wantNil       bool
	}{
		{name: "successful signal", wantNil: true},
		{name: "missing group", signalErr: unix.ESRCH, wantNil: true},
		{name: "zombie leader only", signalErr: permissionErr, leader: darwinKinfoProcPointer(member(4107, 4107, darwinProcessStatusZombie)), members: []unix.KinfoProc{member(4107, 4107, darwinProcessStatusZombie)}, wantNil: true},
		{name: "zombie leader and zombie descendant", signalErr: permissionErr, leader: darwinKinfoProcPointer(member(4107, 4107, darwinProcessStatusZombie)), members: []unix.KinfoProc{member(4107, 4107, darwinProcessStatusZombie), member(4108, 4107, darwinProcessStatusZombie)}, wantNil: true},
		{name: "leader lookup failure", signalErr: permissionErr, leaderErr: inspectionErr, members: []unix.KinfoProc{member(4107, 4107, darwinProcessStatusZombie)}},
		{name: "leader lookup missing", signalErr: permissionErr, members: []unix.KinfoProc{member(4107, 4107, darwinProcessStatusZombie)}},
		{name: "live leader", signalErr: permissionErr, leader: darwinKinfoProcPointer(member(4107, 4107, darwinProcessStatusRunning)), members: []unix.KinfoProc{member(4107, 4107, darwinProcessStatusRunning)}},
		{name: "live descendant", signalErr: permissionErr, leader: darwinKinfoProcPointer(member(4107, 4107, darwinProcessStatusZombie)), members: []unix.KinfoProc{member(4107, 4107, darwinProcessStatusZombie), member(4108, 4107, darwinProcessStatusSleeping)}},
		{name: "missing leader", signalErr: permissionErr, leader: darwinKinfoProcPointer(member(4107, 4107, darwinProcessStatusZombie)), members: []unix.KinfoProc{member(4108, 4107, darwinProcessStatusZombie)}},
		{name: "invalid member pid", signalErr: permissionErr, leader: darwinKinfoProcPointer(member(4107, 4107, darwinProcessStatusZombie)), members: []unix.KinfoProc{member(4107, 4107, darwinProcessStatusZombie), member(0, 4107, darwinProcessStatusZombie)}},
		{name: "wrong group", signalErr: permissionErr, leader: darwinKinfoProcPointer(member(4107, 4108, darwinProcessStatusZombie)), members: []unix.KinfoProc{member(4107, 4108, darwinProcessStatusZombie)}},
		{name: "unknown status", signalErr: permissionErr, leader: darwinKinfoProcPointer(member(4107, 4107, 6)), members: []unix.KinfoProc{member(4107, 4107, 6)}},
		{name: "inspection failure", signalErr: permissionErr, leader: darwinKinfoProcPointer(member(4107, 4107, darwinProcessStatusZombie)), inspectionErr: inspectionErr, members: []unix.KinfoProc{member(4107, 4107, darwinProcessStatusZombie)}},
		{name: "unrelated signal error", signalErr: unix.EINVAL, leader: darwinKinfoProcPointer(member(4107, 4107, darwinProcessStatusZombie)), members: []unix.KinfoProc{member(4107, 4107, darwinProcessStatusZombie)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := resolveNativeDarwinProcessGroupSignalError(
				4107, test.signalErr, test.leader, test.leaderErr, test.members, test.inspectionErr,
			)
			if test.wantNil {
				if got != nil {
					t.Fatalf("resolved error = %v, want nil", got)
				}
				return
			}
			if !errors.Is(got, test.signalErr) {
				t.Fatalf("resolved error = %v, want original %v", got, test.signalErr)
			}
		})
	}
}

func darwinKinfoProcPointer(value unix.KinfoProc) *unix.KinfoProc {
	return &value
}

type darwinBoundedCommandTestPoll struct {
	exited bool
	err    error
}

type darwinBoundedCommandTestMonitor struct {
	events      *[]string
	registerErr error
	status      darwinBoundedCommandLeaderStatus
	statusErr   error
	polls       []darwinBoundedCommandTestPoll
}

func (monitor *darwinBoundedCommandTestMonitor) Register(_ int) error {
	*monitor.events = append(*monitor.events, "register")
	return monitor.registerErr
}

func (monitor *darwinBoundedCommandTestMonitor) Status(_ int) (darwinBoundedCommandLeaderStatus, error) {
	*monitor.events = append(*monitor.events, "status")
	return monitor.status, monitor.statusErr
}

func (monitor *darwinBoundedCommandTestMonitor) Poll(timeout time.Duration) (bool, error) {
	*monitor.events = append(*monitor.events, "poll")
	if timeout != darwinBoundedCommandPollInterval {
		return false, fmt.Errorf("poll timeout = %v", timeout)
	}
	if len(monitor.polls) == 0 {
		return false, errors.New("unexpected poll")
	}
	result := monitor.polls[0]
	monitor.polls = monitor.polls[1:]
	return result.exited, result.err
}

func (monitor *darwinBoundedCommandTestMonitor) Close() error {
	return nil
}

type darwinBoundedCommandTestSystem struct {
	openErr error
}

type darwinZombieBeforeRegisterSystem struct {
	sawZombieBeforeRegister bool
}

type darwinZombieBeforeRegisterMonitor struct {
	inner  darwinBoundedCommandExitMonitor
	owner  *darwinZombieBeforeRegisterSystem
	closed bool
}

func (system *darwinZombieBeforeRegisterSystem) OpenProcessExitMonitor() (darwinBoundedCommandExitMonitor, error) {
	inner, err := (nativeDarwinBoundedCommandSystem{}).OpenProcessExitMonitor()
	if err != nil {
		return nil, err
	}
	return &darwinZombieBeforeRegisterMonitor{inner: inner, owner: system}, nil
}

func (system *darwinZombieBeforeRegisterSystem) SignalProcessGroup(pid int) error {
	return (nativeDarwinBoundedCommandSystem{}).SignalProcessGroup(pid)
}

func (monitor *darwinZombieBeforeRegisterMonitor) Register(pid int) error {
	deadline := time.Now().Add(darwinBoundedCommandTreeExitDelay)
	for time.Now().Before(deadline) {
		status, err := observeNativeDarwinBoundedCommandLeaderStatus(pid)
		if err != nil {
			return err
		}
		if status == darwinBoundedCommandLeaderZombie {
			monitor.owner.sawZombieBeforeRegister = true
			return monitor.inner.Register(pid)
		}
		time.Sleep(time.Millisecond)
	}
	return errors.New("test leader did not become a zombie before registration")
}

func (monitor *darwinZombieBeforeRegisterMonitor) Status(pid int) (darwinBoundedCommandLeaderStatus, error) {
	return monitor.inner.Status(pid)
}

func (monitor *darwinZombieBeforeRegisterMonitor) Poll(timeout time.Duration) (bool, error) {
	return monitor.inner.Poll(timeout)
}

func (monitor *darwinZombieBeforeRegisterMonitor) Close() error {
	if monitor.closed {
		return nil
	}
	monitor.closed = true
	return monitor.inner.Close()
}

func (system *darwinBoundedCommandTestSystem) OpenProcessExitMonitor() (darwinBoundedCommandExitMonitor, error) {
	return nil, system.openErr
}

func (system *darwinBoundedCommandTestSystem) SignalProcessGroup(int) error {
	return nil
}

func maximumDarwinBoundedCommandDuration() time.Duration {
	return darwinBoundedCommandPipeDrainDelay + darwinBoundedCommandTreeExitDelay + 2*time.Second
}

func darwinDescendantCommandFactory(
	mode string,
	pidFile string,
	overflowBytes int,
) func(context.Context, string, ...string) *exec.Cmd {
	return func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(
			ctx,
			os.Args[0],
			"-test.run=^TestDarwinBoundedCommandProcessHelper$",
			"--",
			mode,
			pidFile,
			strconv.Itoa(overflowBytes),
		)
	}
}

func TestDarwinBoundedCommandProcessHelper(t *testing.T) {
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		return
	}
	mode := os.Args[separator+1]
	switch mode {
	case "parent-timeout", "parent-overflow", "parent-exit", "parent-nonzero", "parent-escaped":
		if separator+3 >= len(os.Args) {
			os.Exit(30)
		}
		pidFile := os.Args[separator+2]
		overflowBytes, err := strconv.Atoi(os.Args[separator+3])
		if err != nil || startDarwinPipeHolder(pidFile, mode == "parent-escaped") != nil {
			os.Exit(31)
		}
		if mode == "parent-overflow" {
			_, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", overflowBytes))
		}
		if mode == "parent-exit" {
			os.Exit(0)
		}
		if mode == "parent-nonzero" {
			os.Exit(33)
		}
		if mode == "parent-escaped" {
			os.Exit(0)
		}
		for {
			time.Sleep(time.Hour)
		}
	case "pipe-holder":
		for {
			time.Sleep(time.Hour)
		}
	default:
		os.Exit(32)
	}
}

func startDarwinPipeHolder(pidFile string, escaped bool) error {
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestDarwinBoundedCommandProcessHelper$",
		"--",
		"pipe-holder",
	)
	command.Env = os.Environ()
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if escaped {
		command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	if err := command.Start(); err != nil {
		return err
	}
	return os.WriteFile(pidFile, []byte(strconv.Itoa(command.Process.Pid)), 0o600)
}

func cleanupDarwinEscapedTestProcess(t *testing.T, pid int) {
	t.Helper()
	err := unix.Kill(pid, unix.SIGKILL)
	if err != nil && !errors.Is(err, unix.ESRCH) {
		t.Fatalf("kill escaped test process %d: %v", pid, err)
	}
	assertDarwinTestProcessGone(t, pid)
}

func awaitDarwinTestPID(t *testing.T, path string, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		value, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(value)))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant PID was not recorded at %s", path)
	return 0
}

func awaitDarwinCommandResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(maximumDarwinBoundedCommandDuration()):
		t.Fatal("bounded command did not return")
		return nil
	}
}

func assertDarwinTestProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(darwinBoundedCommandTreeExitDelay + time.Second)
	for time.Now().Before(deadline) {
		err := unix.Kill(pid, 0)
		if errors.Is(err, unix.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant process %d remained after bounded command cleanup", pid)
}
