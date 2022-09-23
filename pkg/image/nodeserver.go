/*
Copyright 2017 The Kubernetes Authors.

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
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang/glog"
	"golang.org/x/net/context"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/kubernetes/pkg/util/mount"

	manifest "github.com/containers/image/v5/manifest"
	csicommon "github.com/kubernetes-csi/drivers/pkg/csi-common"
)

const (
	deviceID   = "deviceID"
	imageStore = "/image-storage"
)

type nodeServer struct {
	*csicommon.DefaultNodeServer
	Timeout  time.Duration
	execPath string
}

func (ns *nodeServer) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	// Check arguments
	if req.GetVolumeCapability() == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume capability missing in request")
	}
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	if len(req.GetTargetPath()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path missing in request")
	}

	image := req.GetVolumeContext()["image"]

	err := ns.setupVolume(req.GetVolumeId(), image)
	if err != nil {
		return nil, err
	}

	targetPath := req.GetTargetPath()
	notMnt, err := mount.New("").IsLikelyNotMountPoint(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			if err = os.MkdirAll(targetPath, 0750); err != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}
			notMnt = true
		} else {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	if !notMnt {
		return &csi.NodePublishVolumeResponse{}, nil
	}

	fsType := req.GetVolumeCapability().GetMount().GetFsType()

	deviceId := ""
	if req.GetPublishContext() != nil {
		deviceId = req.GetPublishContext()[deviceID]
	}

	readOnly := req.GetReadonly()
	volumeId := req.GetVolumeId()
	attrib := req.GetVolumeContext()
	mountFlags := req.GetVolumeCapability().GetMount().GetMountFlags()

	glog.V(4).Infof("target %v\nfstype %v\ndevice %v\nreadonly %v\nvolumeId %v\nattributes %v\n mountflags %v\n",
		targetPath, fsType, deviceId, readOnly, volumeId, attrib, mountFlags)

	options := []string{"bind"}
	if readOnly {
		options = append(options, "ro")
	}

	path := fmt.Sprintf("%s/%s/extracted", imageStore, image)
	mounter := mount.New("")
	if err := mounter.Mount(path, targetPath, "", options); err != nil {
		return nil, err
	}

	return &csi.NodePublishVolumeResponse{}, nil
}

func (ns *nodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	if len(req.GetTargetPath()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path missing in request")
	}
	targetPath := req.GetTargetPath()
	volumeId := req.GetVolumeId()

	// Check that target path is actually still a MountPoint
	notMnt, err := mount.New("").IsLikelyNotMountPoint(targetPath)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !notMnt {
		// Unmounting the image
		err := mount.New("").Unmount(req.GetTargetPath())
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	glog.V(4).Infof("image: volume %s/%s has been unmounted.", targetPath, volumeId)

	err = ns.unsetupVolume(volumeId)
	if err != nil {
		return nil, err
	}
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (ns *nodeServer) extractImage(image string) {
	destinationFolder := fmt.Sprintf("%s/%s", imageStore, image)
	if err := os.MkdirAll(destinationFolder, os.ModePerm); err != nil {
		glog.V(4).Infof("creating dir %s failed %s\n", destinationFolder, err.Error())
		return
	}

	lockFile := fmt.Sprintf("%s/%s.inprogress", imageStore, strings.ReplaceAll(image, "/", "_"))
	file, err := os.Create(lockFile)
	if err != nil {
		glog.V(4).Infof("Lockfile creation %s failed %s\n", lockFile, err.Error())
	}
	defer file.Close()

	glog.V(4).Infof("Copy %s to %s\n", image, destinationFolder)
	source := fmt.Sprintf("docker://%s", image)
	destination := fmt.Sprintf("dir:%s", destinationFolder)
	args := []string{"copy", source, destination}
	ns.execPath = "/bin/skopeo" // FIXME
	_, err = ns.runCmd(args)
	if err != nil {
		glog.V(4).Infof("copy image %s failed %s\n", image, err.Error())
		return
	}

	extractFolder := fmt.Sprintf("%s/extracted", destinationFolder)
	glog.V(4).Infof("Extract %s to %s\n", image, extractFolder)
	if err := os.MkdirAll(extractFolder, os.ModePerm); err != nil {
		glog.V(4).Infof("creating dir %s failed %s\n", extractFolder, err.Error())
		return
	}

	manifestFile, err := os.ReadFile(fmt.Sprintf("%s/manifest.json", destinationFolder))
	if err != nil {
		glog.V(4).Infof("reading file %s failed %s\n", fmt.Sprintf("%s/manifest.json", destinationFolder), err.Error())
		return
	}
	manifest, err := manifest.FromBlob(manifestFile, manifest.GuessMIMEType(manifestFile))
	if err != nil {
		glog.V(4).Infof("reading manifest %s failed %s\n", fmt.Sprintf("%s/manifest.json", destinationFolder), err.Error())
		return
	}
	for _, layer := range manifest.LayerInfos() {
		glog.V(4).Infof("extracting layer %s\n", layer.Digest)
		err = ns.extractTarGz(fmt.Sprintf("%s/%s", destinationFolder, layer.Digest.Encoded()), extractFolder)
		if err != nil {
			glog.V(4).Infof("extracting layer %s failed %s\n", layer.Digest.Encoded(), err.Error())
			return
		}

	}
	os.Remove(lockFile)
}

func (ns *nodeServer) setupVolume(volumeId string, image string) error {
	// TODO document last pull with touch /image-store/lastpulls/imagename:tag
	if _, err := os.Stat(imageStore); !os.IsNotExist(err) {
		destinationFolder := fmt.Sprintf("%s/%s", imageStore, image)
		if _, err := os.Stat(fmt.Sprintf("%s/%s.inprogress", imageStore, strings.ReplaceAll(image, "/", "_"))); !os.IsNotExist(err) {
			msg := fmt.Sprintf("image %s is currently being extracted to %s", image, destinationFolder)
			glog.V(4).Infof("%s\n", msg)
			return fmt.Errorf("%s", msg)
			// TODO handle 3 hour timeout
		} else if _, err := os.Stat(fmt.Sprintf("%s/extracted", destinationFolder)); os.IsNotExist(err) {
			go ns.extractImage(image)
			return fmt.Errorf("image %s pull in progress", image)
		} else {
			glog.V(4).Infof("image %s already pulled\n", image)
			return nil
		}
	} else {
		// TODO Check this already in startup
		return fmt.Errorf("image store unavailable")
	}
}

func (ns *nodeServer) unsetupVolume(volumeId string) error {
	// TODO Consider a setting to cleanup local directory
	return nil
}

func (ns *nodeServer) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (ns *nodeServer) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	return &csi.NodeStageVolumeResponse{}, nil
}

func (ns *nodeServer) runCmd(args []string) ([]byte, error) {
	execPath := ns.execPath
	cmd := exec.Command(execPath, args...)

	return cmd.CombinedOutput()
}

func (ns *nodeServer) extractTarGz(tarball, target string) error {
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
