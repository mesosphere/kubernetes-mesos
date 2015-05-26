package fs

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
)

// ZipWalker returns a filepath.WalkFunc that adds every filesystem node
// to the given *zip.Writer.
func ZipWalker(zw *zip.Writer) filepath.WalkFunc {
	var base string
	return func(path string, info os.FileInfo, err error) error {
		if base == "" {
			base = path
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}

		if header.Name, err = filepath.Rel(base, path); err != nil {
			return err
		} else if info.IsDir() {
			header.Name = header.Name + string(filepath.Separator)
		} else {
			header.Method = zip.Deflate
		}

		w, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}

		_, err = io.Copy(w, f)
		f.Close()
		return err
	}
}
