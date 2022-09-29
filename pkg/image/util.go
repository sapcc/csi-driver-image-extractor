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
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang/glog"
)

type ContainerImage struct {
	Name   string
	Digest string
}

func NewContainerImage(image string) (*ContainerImage, error) {
	digest, err := getImageDigest(image)
	if err != nil {
		return nil, err
	}
	return &ContainerImage{
		Name:   image,
		Digest: digest,
	}, nil
}

func (image ContainerImage) isExtracted() bool {
	//TODO Check digest for updates
	if inProgess, _ := image.isPullInProgress(); inProgess {
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
	return path.Join(extractDir, image.Digest)
}

func (image ContainerImage) getDigestDestination() string {
	return path.Join(digestDir, image.Name)
}

func (image ContainerImage) cleanup() {
	os.Remove(image.getLockFileName())
	os.RemoveAll(image.getCopyDestination())
	os.RemoveAll(image.getExtractDestination())
}

func getImageDigest(image string) (string, error) {
	source := fmt.Sprintf("docker://%s", image)
	args := []string{"inspect"}
	authFile := os.Getenv("REGISTRY_AUTH_FILE")
	if authFile != "" {
		args = append(args, "--authfile", authFile)
	}
	args = append(args, source)
	// TODO Check whether we can use github.com/containers/image/v5 for that
	cmd := exec.Command("/bin/skopeo", args...)
	stdoutStderr, err := cmd.CombinedOutput()
	glog.V(6).Infof("skopeo inspect image %s: %s\n", image, stdoutStderr)
	if err != nil {
		glog.V(4).Infof("skopeo inspect %s failed %s\n", image, err.Error())
		return "", err
	} else {
		type Inspect struct {
			Digest string
		}
		var inspect Inspect
		err := json.Unmarshal([]byte(stdoutStderr), &inspect)
		if err != nil {
			glog.V(4).Infof("unmarshal inspect for %s failed %s\n", image, err.Error())
			return "", err
		}
		// Digest "sha256:790b52558236313f0939403be37e5e5e7c767602975ba3f740ad887a3e28f1ed"
		// extract the last part
		idx := strings.Index(inspect.Digest, ":")
		if idx < 0 {
			return "", fmt.Errorf("digest %s malformed", inspect.Digest)
		}
		return inspect.Digest[idx+1:], nil
	}
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

		switch header.Typeflag {
		case tar.TypeDir:
			if err = os.MkdirAll(path, info.Mode()); err != nil {
				return err
			}

		case tar.TypeLink, 50:
			if err := os.Symlink(header.Linkname, path); err != nil {
				return err
			}

		case tar.TypeReg, tar.TypeRegA:
			file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
			if err != nil {
				return err
			}
			defer file.Close()
			_, err = io.Copy(file, tarReader)
			if err != nil {
				return err
			}

		default:
			glog.V(4).Infof("unhandled tar header type %d for %s\n", header.Typeflag, header.Name)
			continue
		}
	}
	return nil
}
