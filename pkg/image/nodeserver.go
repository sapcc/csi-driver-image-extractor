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
	"fmt"
	"os"
	"os/exec"
	"path"
	"time"

	"github.com/golang/glog"
	"golang.org/x/net/context"
	"k8s.io/mount-utils"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	manifest "github.com/containers/image/v5/manifest"
)

func (ie *ImageExtractor) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	caps := []*csi.NodeServiceCapability{
		{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{
					Type: csi.NodeServiceCapability_RPC_UNKNOWN,
				},
			},
		},
	}

	return &csi.NodeGetCapabilitiesResponse{Capabilities: caps}, nil
}

func (ie *ImageExtractor) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	resp := &csi.NodeGetInfoResponse{
		NodeId: ie.config.NodeID,
	}

	return resp, nil
}

func (ie *ImageExtractor) NodeGetVolumeStats(ctx context.Context, in *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	return &csi.NodeGetVolumeStatsResponse{}, nil
}

func (ie *ImageExtractor) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
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
	containerImage := NewContainerImage(image)

	err := ie.setupVolume(req.GetVolumeId(), containerImage)
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

	path := containerImage.getExtractDestination()
	mounter := mount.New("")
	if err := mounter.Mount(path, targetPath, "", options); err != nil {
		return nil, err
	}

	return &csi.NodePublishVolumeResponse{}, nil
}

func (ie *ImageExtractor) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
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

	err = ie.unsetupVolume(volumeId)
	if err != nil {
		return nil, err
	}
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (ie ImageExtractor) extractImage(image *ContainerImage) {
	if err := os.MkdirAll(progressDir, os.ModePerm); err != nil {
		glog.V(4).Infof("creating dir %s failed %s\n", progressDir, err.Error())
		return
	}
	// Ensure that image is only processed once
	touchFile(image.getLockFileName(), false)

	copyDir := image.getCopyDestination()
	if err := os.MkdirAll(copyDir, os.ModePerm); err != nil {
		glog.V(4).Infof("creating dir %s failed %s\n", copyDir, err.Error())
		return
	}

	glog.V(4).Infof("Copy %s to %s\n", image.Name, copyDir)
	source := fmt.Sprintf("docker://%s", image.Name)
	destination := fmt.Sprintf("dir:%s", copyDir)
	args := []string{"copy"}
	authFile := os.Getenv("REGISTRY_AUTH_FILE")
	if authFile != "" {
		args = append(args, "--authfile", authFile)
	}
	args = append(args, source, destination)
	// TODO Check whether we can use github.com/containers/image/v5 for that
	cmd := exec.Command("/bin/skopeo", args...)
	stdoutStderr, err := cmd.CombinedOutput()
	glog.V(4).Infof("skopeo copy image %s: %s\n", image.Name, stdoutStderr)
	if err != nil {
		glog.V(4).Infof("copy image %s failed %s\n", image.Name, err.Error())
		return
	}

	extractDir := image.getExtractDestination()
	glog.V(4).Infof("Extract %s to %s\n", image.Name, extractDir)
	if err := os.MkdirAll(extractDir, os.ModePerm); err != nil {
		glog.V(4).Infof("creating dir %s failed %s\n", extractDir, err.Error())
		return
	}

	manifestFile, err := os.ReadFile(path.Join(copyDir, "manifest.json"))
	if err != nil {
		glog.V(4).Infof("reading file %s failed %s\n", path.Join(copyDir, "manifest.json", copyDir), err.Error())
		return
	}
	manifest, err := manifest.FromBlob(manifestFile, manifest.GuessMIMEType(manifestFile))
	if err != nil {
		glog.V(4).Infof("reading manifest %s failed %s\n", path.Join(copyDir, "manifest.json", copyDir), err.Error())
		return
	}
	for _, layer := range manifest.LayerInfos() {
		glog.V(4).Infof("extracting layer %s\n", layer.Digest)
		err = extractTarGz(path.Join(copyDir, layer.Digest.Encoded()), extractDir)
		if err != nil {
			glog.V(4).Infof("extracting layer %s failed %s\n", layer.Digest.Encoded(), err.Error())
			return
		}

	}

	glog.V(4).Infof(" %s ready for consumption\n", image.Name)
	os.Remove(image.getLockFileName())
}

func (ie *ImageExtractor) setupVolume(volumeId string, image *ContainerImage) error {
	image.recordImageRequest()
	if isPullInProgress, since := image.isPullInProgress(); isPullInProgress {
		msg := fmt.Sprintf("image %s is beeing processed since %s", image.Name, since.Format(time.RFC3339Nano))
		glog.V(4).Infof("%s\n", msg)
		if time.Since(since) > ie.config.MaxPublishDuration {
			image.cleanup()
		}
		return fmt.Errorf("%s", msg)
	} else if !image.isExtracted() {
		go ie.extractImage(image)
		return fmt.Errorf("image pull %s started", image.Name)
	} else {
		glog.V(4).Infof("image %s already pulled\n", image.Name)
		return nil
	}
}

func (ie *ImageExtractor) unsetupVolume(volumeId string) error {
	// TODO Consider a setting to cleanup local directory
	return nil
}

func (ie *ImageExtractor) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (ie *ImageExtractor) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	return &csi.NodeStageVolumeResponse{}, nil
}

func (ie *ImageExtractor) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	return &csi.NodeExpandVolumeResponse{}, nil
}
