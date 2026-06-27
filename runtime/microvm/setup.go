//go:build linux

package microvm

// ABOUTME: Setup/IsReady for the microvm backend — builds yoloai-base + the microvm
// ABOUTME: image layer via Docker, then converts the image to a golden ext4 rootfs
// ABOUTME: (+ kernel/initrd) inside a throwaway container so mkfs runs as root.

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/sysexec"
	dockerrt "github.com/kstenerud/yoloai/runtime/docker"
	"github.com/kstenerud/yoloai/yoerrors"
)

const (
	// baseImageRef is the shared base image built by the Docker pipeline. The
	// docker, containerd, and microvm backends all build the same one and
	// serialize on the same base lock.
	baseImageRef = "yoloai-base"

	// microvmImageRef is the microvm layer (FROM yoloai-base + kernel + guest
	// agent + init), the image the golden rootfs is converted from.
	microvmImageRef = "yoloai-base-microvm"

	// goldenRootfsName / initrdName are the cached conversion artifacts under
	// the backend's DataDir/microvm dir (kernelFileName is in microvm.go).
	goldenRootfsName = "rootfs.ext4"
	initrdName       = "initrd.img"
)

// microvmDir is the per-host cache dir holding the golden rootfs, kernel, and initrd.
func (r *Runtime) microvmDir() string { return filepath.Join(r.layout.DataDir, backendSubdir) }

// goldenRootfsPath is the read-only ext4 base every sandbox boots via an overlay.
func (r *Runtime) goldenRootfsPath() string { return filepath.Join(r.microvmDir(), goldenRootfsName) }

// initrdPath is the extracted distro initrd passed to QEMU's -initrd.
func (r *Runtime) initrdPath() string { return filepath.Join(r.microvmDir(), initrdName) }

// Setup builds the microvm image and converts it to a bootable golden rootfs.
// It mirrors the containerd backend: yoloai-base is built by Docker (the agent
// CLIs live there), so microvm needs Docker at build time. The microvm layer
// adds the distro kernel + guest agent + init, and the conversion to ext4 runs
// inside a throwaway container so mkfs writes root-owned inodes without any host
// privilege. sourceDir backs the build-inputs checksum (relink-skip fast path).
func (r *Runtime) Setup(ctx context.Context, layout config.Layout, sourceDir string, output io.Writer, logger *slog.Logger, force bool) error {
	// Serialize against every other yoloai-base builder (docker/containerd/microvm).
	unlock, err := dockerrt.AcquireBaseLock(layout, baseImageRef)
	if err != nil {
		return err
	}
	defer unlock()

	if !force {
		if ready, _ := r.IsReady(ctx); ready && !dockerrt.NeedsBuild(layout, sourceDir) {
			return nil
		}
	}

	dockerBin, err := exec.LookPath("docker")
	if err != nil {
		return yoerrors.NewDependencyError("docker is required to build the microvm image " +
			"(the agent CLIs are baked by the Docker pipeline, then the image is converted to a VM rootfs)\n" +
			"  Install Docker: https://docs.docker.com/get-docker/")
	}

	// 1. Build the shared yoloai-base image (same context as docker/containerd).
	baseCtx, err := dockerrt.CreateBuildContext()
	if err != nil {
		return fmt.Errorf("create base build context: %w", err)
	}
	fmt.Fprintln(output, "Building yoloai-base image with Docker (this may take a few minutes)...") //nolint:errcheck // best-effort output
	logger.Info("building yoloai-base image for microvm")
	if err := r.dockerBuild(ctx, dockerBin, baseImageRef, baseCtx, output); err != nil {
		return fmt.Errorf("build yoloai-base: %w", err)
	}

	// 2. Build the microvm layer (FROM yoloai-base + kernel + guest agent + init).
	mvCtx, err := microvmBuildContext()
	if err != nil {
		return fmt.Errorf("create microvm build context: %w", err)
	}
	fmt.Fprintln(output, "Building microvm image layer (distro kernel + guest agent + init)...") //nolint:errcheck // best-effort output
	if err := r.dockerBuild(ctx, dockerBin, microvmImageRef, mvCtx, output); err != nil {
		return fmt.Errorf("build %s: %w", microvmImageRef, err)
	}

	// 3. Convert to a golden ext4 + extract kernel/initrd, inside the image so
	//    mkfs runs as root.
	if err := r.convert(ctx, dockerBin, output); err != nil {
		return err
	}

	dockerrt.RecordBuildChecksum(layout, sourceDir)
	fmt.Fprintln(output, "microvm image ready.") //nolint:errcheck // best-effort output
	return nil
}

// IsReady reports whether the golden rootfs, kernel, and initrd are all present.
func (r *Runtime) IsReady(_ context.Context) (bool, error) {
	for _, p := range []string{r.goldenRootfsPath(), r.kernelPath(), r.initrdPath()} {
		if _, err := os.Stat(p); err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, err
		}
	}
	return true, nil
}

// dockerBuild runs `docker build -t <tag> -f Dockerfile -` with the given build
// context tar streamed on stdin (matches the containerd backend's invocation).
func (r *Runtime) dockerBuild(ctx context.Context, dockerBin, tag string, buildContext io.Reader, output io.Writer) error {
	cmd := sysexec.CommandContext(ctx, r.layout.Env().EnvForDockerBuild(), dockerBin, "build", "-t", tag, "-f", "Dockerfile", "-")
	cmd.Stdin = buildContext
	cmd.Stdout = output
	cmd.Stderr = output
	return cmd.Run()
}

// convert runs the in-container conversion: a throwaway container from the
// microvm image executes microvm-convert as root, writing the golden ext4 +
// kernel + initrd into the host-mounted microvm dir.
func (r *Runtime) convert(ctx context.Context, dockerBin string, output io.Writer) error {
	dir := r.microvmDir()
	if err := fileutil.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create microvm dir: %w", err)
	}
	fmt.Fprintln(output, "Converting image to microvm rootfs (golden ext4 + kernel/initrd)...") //nolint:errcheck // best-effort output
	// --entrypoint overrides yoloai-base's agent entrypoint (entrypoint.py); without
	// it `docker run <image> microvm-convert` passes the script name as an argument
	// to the entrypoint rather than executing it.
	cmd := sysexec.CommandContext(ctx, r.layout.Env().EnvForDockerExec(), dockerBin,
		"run", "--rm", "--entrypoint", "microvm-convert", "-v", dir+":/out", microvmImageRef)
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("convert microvm rootfs: %w", err)
	}
	return nil
}

// microvmBuildContext assembles an in-memory tar build context holding the
// embedded FROM-yoloai-base Dockerfile and the conversion script.
func microvmBuildContext() (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	files := []struct {
		name string
		data []byte
		mode int64
	}{
		{"Dockerfile", embeddedDockerfile, 0o644},
		{"microvm-convert.sh", embeddedConvertScript, 0o755},
	}
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.data))}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return &buf, nil
}
