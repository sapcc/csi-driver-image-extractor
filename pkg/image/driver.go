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
	"errors"
	"fmt"
	"os"
	"path"
	"time"

	"github.com/golang/glog"
)

type ImageExtractor struct {
	config Config
}

type Config struct {
	DriverName         string
	Endpoint           string
	NodeID             string
	VendorVersion      string
	ImageStoreDir      string
	MaxPublishDuration time.Duration
}

var (
	progressDir string
	requestDir  string
	copyDir     string
	extractDir  string
)

func NewImageExtractor(cfg Config) (*ImageExtractor, error) {
	if cfg.DriverName == "" {
		return nil, errors.New("no driver name provided")
	}

	if cfg.Endpoint == "" {
		return nil, errors.New("no driver endpoint provided")
	}

	if cfg.NodeID == "" {
		return nil, errors.New("no node id provided")
	}

	if cfg.ImageStoreDir == "" {
		return nil, errors.New("no image store dir provided")
	}

	if cfg.MaxPublishDuration == 0 {
		return nil, errors.New("no max publish duration provided")
	}

	if _, err := os.Stat(cfg.ImageStoreDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("image store %s does not exist", cfg.ImageStoreDir)
	} else {
		// Ensuring folder structure
		progressDir = path.Join(cfg.ImageStoreDir, "inprogress")
		requestDir = path.Join(cfg.ImageStoreDir, "request")
		copyDir = path.Join(cfg.ImageStoreDir, "copy")
		extractDir = path.Join(cfg.ImageStoreDir, "extract")

		dirs := [4]string{
			progressDir,
			requestDir,
			copyDir,
			extractDir,
		}
		for _, dir := range dirs {
			if err := os.MkdirAll(dir, os.ModePerm); err != nil {
				return nil, fmt.Errorf("creating dir %s failed %s", dir, err.Error())
			}
		}
	}

	glog.Infof("Driver: %v ", cfg.DriverName)
	glog.Infof("Version: %s", cfg.VendorVersion)
	glog.Infof("ImageStoreDir: %s ", cfg.ImageStoreDir)
	glog.Infof("MaxPublishDuration: %s", cfg.MaxPublishDuration)

	ie := &ImageExtractor{
		config: cfg,
	}

	return ie, nil
}

func (ie *ImageExtractor) Run() error {
	s := NewNonBlockingGRPCServer()
	// ImageExtractor itself implements ControllerServer, NodeServer, and IdentityServer.
	s.Start(ie.config.Endpoint, ie, ie, ie)
	s.Wait()

	return nil
}
