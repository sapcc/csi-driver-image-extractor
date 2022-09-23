# csi-driver-image-extractor

CSI driver that uses a container image as transport medium for content, which should be consumed as volume.

## How it works:

This driver was inspired by https://github.com/kubernetes-csi/csi-driver-image-populator, but with tougher requirements regarding container image sizes.

It uses a PersitentVolume as the destination for a copy of the the container image and extracts it's layers there. The extracted content will be provided to the Pod as VolumeMount.
If one uses a multi-node attachable `RWX` PersitentVolume (nfs) the following advantages are given:
* Pulling a container image only needs to happens once in a cluster
* Large container images do not need to be pulled on every node it is requested, hence saving root disk space

Processing large container images will exceed the normal processing times one would expected for provisioning a volume. `csi-driver-image-extractor` has built-in measures to ensure pulling the same image happens only once and the Kubernetes retry mechanism will catch up after the container image is consumable.

## Usage:

**This is a prototype driver. Do not use for production**

### Build image-extractor-plugin binary
```
$ make build
```

### Build image-extractor-plugin container
```
$ make container
```

### Installing into Kubernetes
```
$ deploy/deploy-image-extractor.sh
```

### Example Usage in Kubernetes
```
apiVersion: v1
kind: Pod
metadata:
  name: test
spec:
  containers:
  - name: main
    image: busybox
    volumeMount:
    - name: data
      mountPath: /container-image-data
  volumes:
  - name: data
    csi:
      driver: image.csi.cnmp.sap
      volumeAttributes:
          image: registry/name:tag
```

### Start Image driver manually
```
$ sudo ./bin/image-extractor-plugin --endpoint tcp://127.0.0.1:10000 --nodeid CSINode -v=5
```
