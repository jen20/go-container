package container

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jen20/go-container/internal/testenv"
)

// The tests in this file create, run, and delete containers. They run only
// when both GO_CONTAINER_TEST_LIVE and GO_CONTAINER_TEST_WRITE are set. The
// tests that boot a container VM also need GO_CONTAINER_TEST_VM. They need
// the image in testImage in the local image store; pull it with
// `container image pull debian:trixie-slim`. Each test deletes the
// containers that it creates.

const testImage = "docker.io/library/debian:trixie-slim"

type lifecycleEnv struct {
	c       *Client
	image   ImageDescription
	kernel  Kernel
	network string
}

func setupLifecycle(t *testing.T) *lifecycleEnv {
	t.Helper()
	testenv.RequireWrite(t)
	c := liveClient(t)
	ctx := liveContext(t)
	img, err := c.GetImage(ctx, testImage)
	if errors.Is(err, ErrNotFound) {
		t.Skipf("%s is not in the local image store", testImage)
	}
	if err != nil {
		t.Fatal(err)
	}
	kernel, err := c.DefaultKernel(ctx, LinuxArm64)
	if err != nil {
		t.Fatal(err)
	}
	networks, err := c.ListNetworks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	env := &lifecycleEnv{c: c, image: *img, kernel: *kernel}
	for _, n := range networks {
		if n.IsBuiltin() {
			env.network = n.ID
		}
	}
	if env.network == "" {
		t.Fatal("no builtin network")
	}
	return env
}

// create creates a container that runs args with /bin/sh -c and registers
// its deletion.
func (e *lifecycleEnv) create(t *testing.T, script string) string {
	t.Helper()
	b := make([]byte, 4)
	rand.Read(b)
	id := "go-container-test-" + hex.EncodeToString(b)
	proc := NewProcessConfiguration("/bin/sh", []string{"-c", script},
		[]string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"})
	config := NewConfiguration(id, e.image, proc)
	config.Resources.CPUs = 1
	config.Resources.MemoryInBytes = 512 * 1024 * 1024
	config.Networks = []AttachmentConfiguration{DefaultAttachment(e.network, id, "")}
	if err := e.c.CreateContainer(liveContext(t), config, e.kernel, nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := e.c.DeleteContainer(ctx, id, true); err != nil {
			t.Errorf("DeleteContainer(%s): %v", id, err)
		}
	})
	return id
}

// pipe returns an os.Pipe whose read end collects into a string.
func pipe(t *testing.T) (w *os.File, read func() string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 1)
	go func() {
		b, err := io.ReadAll(r)
		if err != nil {
			t.Errorf("read pipe: %v", err)
		}
		checkClose(t, r)
		done <- string(b)
	}()
	return w, func() string {
		select {
		case s := <-done:
			return s
		case <-time.After(10 * time.Second):
			t.Error("stdio pipe was not closed")
			return ""
		}
	}
}

func TestLiveRunToExit(t *testing.T) {
	testenv.RequireVM(t)
	e := setupLifecycle(t)
	ctx := liveContext(t)
	id := e.create(t, "echo to-stdout; echo to-stderr >&2; exit 3")

	if err := e.c.CreateContainer(ctx, NewConfiguration(id, e.image, NewProcessConfiguration("/bin/true", nil, nil)), e.kernel, nil); !errors.Is(err, ErrExists) {
		t.Errorf("second create with the same ID gave %v, want ErrExists", err)
	}

	stdout, readOut := pipe(t)
	stderr, readErr := pipe(t)
	proc, err := e.c.BootstrapContainer(ctx, id, Stdio{Stdout: stdout, Stderr: stderr}, nil)
	checkClose(t, stdout)
	checkClose(t, stderr)
	if err != nil {
		t.Fatalf("BootstrapContainer: %v", err)
	}
	status, err := proc.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if status.Code != 3 {
		t.Errorf("exit code %d, want 3", status.Code)
	}
	if time.Since(status.ExitedAt) > time.Minute {
		t.Errorf("exitedAt %v is not recent", status.ExitedAt)
	}
	if got := readOut(); got != "to-stdout\n" {
		t.Errorf("stdout %q", got)
	}
	if got := readErr(); got != "to-stderr\n" {
		t.Errorf("stderr %q", got)
	}

	output, boot, err := e.c.ContainerLogs(ctx, id)
	if err != nil {
		t.Fatalf("ContainerLogs: %v", err)
	}
	defer checkClose(t, output)
	defer checkClose(t, boot)
	bootLog, _ := io.ReadAll(boot)
	t.Logf("boot log has %d bytes", len(bootLog))
}

func TestLiveExecAndStop(t *testing.T) {
	testenv.RequireVM(t)
	e := setupLifecycle(t)
	ctx := liveContext(t)
	id := e.create(t, "exec sleep 300")

	initProc, err := e.c.BootstrapContainer(ctx, id, Stdio{}, nil)
	if err != nil {
		t.Fatalf("BootstrapContainer: %v", err)
	}
	if err := initProc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	snap, err := e.c.GetContainer(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Status != StatusRunning || len(snap.Networks) != 1 || !snap.Networks[0].IPv4Address.IsValid() {
		t.Fatalf("running container: status %s, networks %+v", snap.Status, snap.Networks)
	}

	// A Wait that gives up early returns the context error, and the client
	// still works afterwards.
	short, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	_, err = initProc.Wait(short)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Wait with a short deadline gave %v", err)
	}

	stdout, readOut := pipe(t)
	exec, err := e.c.CreateProcess(ctx, id, "exec-1",
		NewProcessConfiguration("/bin/sh", []string{"-c", "echo $((6*7)); exit 0"}, []string{"PATH=/usr/bin:/bin"}),
		Stdio{Stdout: stdout})
	checkClose(t, stdout)
	if err != nil {
		t.Fatalf("CreateProcess: %v", err)
	}
	if st, err := exec.Run(ctx); err != nil || st.Code != 0 {
		t.Fatalf("exec Run: %+v %v", st, err)
	}
	if got := strings.TrimSpace(readOut()); got != "42" {
		t.Errorf("exec output %q, want 42", got)
	}

	stats, err := e.c.ContainerStats(ctx, id)
	if err != nil {
		t.Errorf("ContainerStats: %v", err)
	} else if stats.MemoryUsageBytes == nil {
		t.Errorf("stats have no memory usage: %+v", stats)
	}

	if err := e.c.StopContainer(ctx, id, StopOptions{TimeoutInSeconds: 2}); err != nil {
		t.Fatalf("StopContainer: %v", err)
	}
	snap, err = e.c.GetContainer(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Status != StatusStopped {
		t.Errorf("status after stop is %s", snap.Status)
	}
}

func TestLiveKillCopyExport(t *testing.T) {
	testenv.RequireVM(t)
	e := setupLifecycle(t)
	ctx := liveContext(t)
	id := e.create(t, "exec sleep 300")
	proc, err := e.c.BootstrapContainer(ctx, id, Stdio{}, nil)
	if err != nil {
		t.Fatalf("BootstrapContainer: %v", err)
	}
	wait, err := proc.StartWait(ctx)
	if err != nil {
		t.Fatalf("StartWait: %v", err)
	}

	dir := t.TempDir()
	src := dir + "/in.txt"
	want := "copied through the container\n"
	if err := os.WriteFile(src, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.c.CopyIn(ctx, id, src, "/tmp/go-container/in.txt", 0o600, true); err != nil {
		t.Fatalf("CopyIn: %v", err)
	}
	if err := e.c.CopyOut(ctx, id, "/tmp/go-container/in.txt", dir+"/out/out.txt", true); err != nil {
		t.Fatalf("CopyOut: %v", err)
	}
	if got, err := os.ReadFile(dir + "/out/out.txt"); err != nil || string(got) != want {
		t.Fatalf("copied file %q, %v", got, err)
	}

	if err := e.c.ExportContainer(ctx, id, dir+"/rootfs.tar"); err != nil {
		t.Errorf("ExportContainer: %v", err)
	} else if fi, err := os.Stat(dir + "/rootfs.tar"); err != nil || fi.Size() < 1<<20 {
		t.Errorf("exported archive: %v, %v", fi, err)
	}

	if err := e.c.KillContainer(ctx, id, "KILL"); err != nil {
		t.Fatalf("KillContainer: %v", err)
	}
	st, err := wait()
	if err != nil {
		t.Fatalf("wait after kill: %v", err)
	}
	if st.Code != 128+9 {
		t.Errorf("exit code after SIGKILL is %d, want 137", st.Code)
	}
}

func TestLiveStartWaitError(t *testing.T) {
	e := setupLifecycle(t)
	ctx := liveContext(t)
	// The process was never created, so the start fails. StartWait must
	// return the error and not block on its wait request.
	done := make(chan error, 1)
	go func() {
		_, err := e.c.Process("go-container-no-such-container", "p").StartWait(ctx)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("StartWait of a missing container succeeded")
		}
		t.Logf("StartWait error: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("StartWait blocked after a failed start")
	}
}
