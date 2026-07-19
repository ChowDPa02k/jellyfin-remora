package procmanager

import (
	"bytes"
	"io"
	"testing"
)

type testGenerationFactory struct {
	bytes.Buffer
	created int
	scoped  io.Writer
}

func (w *testGenerationFactory) NewGenerationWriter() io.Writer {
	w.created++
	return w.scoped
}

func TestBeginConsoleGenerationSharesScopedWriterForCombinedStream(t *testing.T) {
	scoped := &bytes.Buffer{}
	factory := &testGenerationFactory{scoped: scoped}
	stdout, stderr := beginConsoleGeneration(factory, factory)
	if factory.created != 1 || stdout != scoped || stderr != scoped {
		t.Fatalf("created=%d stdout=%T stderr=%T", factory.created, stdout, stderr)
	}
}
