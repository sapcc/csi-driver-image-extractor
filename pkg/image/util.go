/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package image

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type ContainerImage struct {
	Name string
}

func NewContainerImage(image string) *ContainerImage {
	return &ContainerImage{
		Name: image,
	}
}

func (image ContainerImage) isExtracted() bool {
	//TODO Check digest for updates
	if inProgess, _ := image.isPullInProgress(); !inProgess {
		return false
	}
	if _, err := os.Stat(image.getExtractDestination()); os.IsNotExist(err) {
		return false
	}
	return true
}

func (image ContainerImage) isPullInProgress() (bool, time.Time) {
	var lastModified time.Time
	if file, err := os.Stat(image.getLockFileName()); !os.IsNotExist(err) {
		lastModified = file.ModTime()
	}

	return !lastModified.IsZero(), lastModified
}

func (image ContainerImage) recordImageRequest() error {
	// Ensure request dir
	if err := os.MkdirAll(requestDir, os.ModePerm); err != nil {
		return err
	}

	// Document last request time for the requested image
	return touchFile(image.getRequestFileName(), true)
}

func (image ContainerImage) getFileName() string {
	return strings.ReplaceAll(image.Name, "/", "_")
}

func (image ContainerImage) getLockFileName() string {
	return path.Join(progressDir, image.getFileName())
}

func (image ContainerImage) getRequestFileName() string {
	return path.Join(requestDir, image.getFileName())
}

func (image ContainerImage) getCopyDestination() string {
	return path.Join(copyDir, image.Name)
}

func (image ContainerImage) getExtractDestination() string {
	return path.Join(extractDir, image.Name)
}

func (image ContainerImage) cleanup() {
	// TODO remove lockfile, download, extract
	os.Remove(image.getLockFileName())
	os.RemoveAll(image.getCopyDestination())
	os.RemoveAll(image.getExtractDestination())
}

func touchFile(fileName string, updateTimes bool) error {
	if _, err := os.Stat(fileName); os.IsNotExist(err) {
		file, err := os.Create(fileName)
		if err != nil {
			return err
		}
		defer file.Close()
	} else if updateTimes {
		currentTime := time.Now().Local()
		err = os.Chtimes(fileName, currentTime, currentTime)
		if err != nil {
			return err
		}
	}
	return nil
}

func extractTarGz(tarball, target string) error {
	reader, err := os.Open(tarball)
	if err != nil {
		return err
	}
	defer reader.Close()

	uncompressedStream, err := gzip.NewReader(reader)
	if err != nil {
		return err
	}
	defer uncompressedStream.Close()

	tarReader := tar.NewReader(uncompressedStream)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}

		path := filepath.Join(target, header.Name)
		info := header.FileInfo()
		if info.IsDir() {
			if err = os.MkdirAll(path, info.Mode()); err != nil {
				return err
			}
			continue
		}

		file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(file, tarReader)
		if err != nil {
			return err
		}
	}
	return nil
}
