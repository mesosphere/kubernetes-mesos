package archive

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Tar creates a tar archive with all the given files.
func Tar(files ...string) (*tar.Reader, error) {
	var buf bytes.Buffer
	arch := tar.NewWriter(&buf)
	for _, file := range files {
		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		defer f.Close()

		if fi, err := f.Stat(); err != nil {
			return nil, err
		} else if hdr, err := tar.FileInfoHeader(fi, file); err != nil {
			return nil, err
		} else if err := arch.WriteHeader(hdr); err != nil {
			return nil, err
		} else if _, err := io.Copy(arch, f); err != nil {
			return nil, err
		}
	}
	return tar.NewReader(&buf), nil
}

// Untar writes all entries in the given tar.Reader to the dir specified.
func Untar(rd *tar.Reader, dir string) error {
	if fi, err := os.Stat(filepath.Clean(dir)); err != nil {
		return err
	} else if !fi.IsDir() {
		return fmt.Errorf("untar: %q isn't a directory", dir)
	}

	for {
		hdr, err := rd.Next()
		if err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}

		path := filepath.Clean(filepath.Join(dir, hdr.Name))
		f, err := os.Create(path)
		if err != nil {
			return err
		}
		defer f.Close()

		if _, err := io.Copy(f, io.LimitReader(rd, hdr.Size)); err != nil {
			return err
		}
	}
}
