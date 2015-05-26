package fs_test

import (
	"archive/zip"
	"bytes"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/mesosphere/kubernetes-mesos/pkg/fs"
)

func TestZipWalker(t *testing.T) {
	dir, err := ioutil.TempDir(os.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}

	tree := map[string]string{"a/b/c": "12345", "a/b/d": "54321", "a/e": "00000"}
	for path, content := range tree {
		path = filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(path), os.ModeTemporary|0700); err != nil {
			t.Fatal(err)
		} else if err = ioutil.WriteFile(path, []byte(content), 0700); err != nil {
			t.Fatal(err)
		}
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := filepath.Walk(dir, fs.ZipWalker(zw)); err != nil {
		t.Fatal(err)
	} else if err = zw.Close(); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}

	for _, file := range zr.File {
		if rc, err := file.Open(); err != nil {
			t.Fatal(err)
		} else if got, err := ioutil.ReadAll(rc); err != nil {
			t.Error(err)
		} else if want := []byte(tree[file.Name]); !bytes.Equal(got, want) {
			t.Errorf("%s\ngot:  %s\nwant: %s", file.Name, got, want)
		}
	}
}
