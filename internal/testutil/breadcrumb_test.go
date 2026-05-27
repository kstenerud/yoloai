// ABOUTME: Unit tests for the integration-TestMain breadcrumb printer.
package testutil

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBreadcrumb_PrintsStartAndDone(t *testing.T) {
	var buf bytes.Buffer
	step := breadcrumbWriter("sandbox", &buf)

	called := false
	step("connecting to docker", func() { called = true })

	assert.True(t, called, "step function should invoke the body")

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if assert.Len(t, lines, 2, "expected start and done lines") {
		assert.Equal(t, "integration[sandbox]: connecting to docker...", lines[0])
		assert.Regexp(t, `^integration\[sandbox\]: connecting to docker done \(\d+(\.\d+)?(ns|µs|ms|s)\)$`, lines[1])
	}
}

func TestBreadcrumb_DistinctPkgLabels(t *testing.T) {
	var buf bytes.Buffer
	for _, pkg := range []string{"sandbox", "docker", "cli"} {
		step := breadcrumbWriter(pkg, &buf)
		step("step", func() {})
	}

	out := buf.String()
	assert.Contains(t, out, "integration[sandbox]:")
	assert.Contains(t, out, "integration[docker]:")
	assert.Contains(t, out, "integration[cli]:")
}
