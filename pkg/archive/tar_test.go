package archive_test

import (
	"bytes"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/mesosphere/kubernetes-mesos/pkg/archive"
)

func TestTar(t *testing.T) {
	names := []string{"foo.txt", "bar.txt", "baz.txt"}
	contents := []string{"bar", "foo", "moo"}

	dir := makeDir(t)
	for i := range names {
		names[i] = filepath.Join(dir, names[i])
		makeFile(t, names[i], contents[i])
	}

	reader, err := archive.Tar(names...)
	if err != nil {
		t.Fatal(err)
	}

	for i := range names {
		hdr, err := reader.Next()
		if err != nil {
			t.Fatal(err)
		}

		if got, want := hdr.Name, filepath.Base(names[i]); got != want {
			t.Errorf("name: got: %v, want: %v", got, want)
		}

		got, err := ioutil.ReadAll(io.LimitReader(reader, hdr.Size))
		if err != nil {
			t.Fatal(err)
		}

		if want := []byte(contents[i]); !bytes.Equal(got, want) {
			t.Fatalf("content\ngot:  %v\nwant: %v", got, want)
		}
	}
}

func makeDir(t *testing.T) string {
	dir, err := ioutil.TempDir(os.TempDir(), "k8sm-archive")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func makeFile(t *testing.T, name, contents string) {
	if err := ioutil.WriteFile(name, []byte(contents), 0555); err != nil {
		t.Fatalf("Unable to write test file %#v", err)
	}
}
