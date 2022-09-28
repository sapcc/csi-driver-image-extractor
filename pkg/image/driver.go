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

	"github.com/golang/glog"
)

type ImageExtractor struct {
	config Config
}

type Config struct {
	DriverName    string
	Endpoint      string
	NodeID        string
	VendorVersion string
}

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

	glog.Infof("Driver: %v ", cfg.DriverName)
	glog.Infof("Version: %s", cfg.VendorVersion)

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
