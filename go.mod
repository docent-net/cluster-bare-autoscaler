module github.com/docent-net/cluster-bare-autoscaler

go 1.23

toolchain go1.23.3

replace github.com/docent-net/cluster-bare-autoscaler/metrics => ./metrics

replace github.com/docent-net/cluster-bare-autoscaler/version => ./version

replace k8s.io/apiextensions-apiserver => github.com/kubernetes/apiextensions-apiserver v0.31.3

replace k8s.io/cli-runtime => github.com/kubernetes/cli-runtime v0.31.3

replace k8s.io/cloud-provider => github.com/kubernetes/cloud-provider v0.31.3

replace k8s.io/dynamic-resource-allocation => github.com/kubernetes/dynamic-resource-allocation v0.31.3

replace k8s.io/cluster-bootstrap => github.com/kubernetes/cluster-bootstrap v0.31.3

replace k8s.io/component-helpers => github.com/kubernetes/component-helpers v0.31.3

replace k8s.io/controller-manager => github.com/kubernetes/controller-manager v0.31.3

replace k8s.io/cri-api => github.com/kubernetes/cri-api v0.31.3

replace k8s.io/cri-client => github.com/kubernetes/cri-client v0.31.3

replace k8s.io/csi-translation-lib => github.com/kubernetes/csi-translation-lib v0.31.3

replace k8s.io/endpointslice => github.com/kubernetes/endpointslice v0.31.3

replace k8s.io/kube-aggregator => github.com/kubernetes/kube-aggregator v0.31.3

replace k8s.io/kube-controller-manager => github.com/kubernetes/kube-controller-manager v0.31.3

replace k8s.io/kube-proxy => github.com/kubernetes/kube-proxy v0.31.3

replace k8s.io/kube-scheduler => github.com/kubernetes/kube-scheduler v0.31.3

replace k8s.io/kubectl => github.com/kubernetes/kubectl v0.31.3

replace k8s.io/kubelet => github.com/kubernetes/kubelet v0.31.3

replace k8s.io/metrics => github.com/kubernetes/metrics v0.31.3

replace k8s.io/mount-utils => github.com/kubernetes/mount-utils v0.31.3

replace k8s.io/pod-security-admission => github.com/kubernetes/pod-security-admission v0.31.3

replace k8s.io/sample-apiserver => github.com/kubernetes/sample-apiserver v0.31.3

require (
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.10.0
	k8s.io/apiserver v0.31.3
	k8s.io/component-base v0.31.3
	k8s.io/klog/v2 v2.130.1
	k8s.io/kubernetes v1.31.3
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/blang/semver/v4 v4.0.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/fxamacker/cbor/v2 v2.7.0 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-logr/zapr v1.3.0 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/compress v1.17.11 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/prometheus/client_golang v1.20.5 // indirect
	github.com/prometheus/client_model v0.6.1 // indirect
	github.com/prometheus/common v0.60.1 // indirect
	github.com/prometheus/procfs v0.15.1 // indirect
	github.com/spf13/cobra v1.8.1 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/net v0.31.0 // indirect
	golang.org/x/sys v0.27.0 // indirect
	golang.org/x/text v0.20.0 // indirect
	google.golang.org/protobuf v1.35.2 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/apiextensions-apiserver v0.0.0 // indirect
	k8s.io/apimachinery v0.31.3 // indirect
	k8s.io/client-go v0.31.3 // indirect
	k8s.io/utils v0.0.0-20241104163129-6fe5fd82f078 // indirect
	sigs.k8s.io/json v0.0.0-20241014173422-cfa47c3a1cc8 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.4.3 // indirect
	sigs.k8s.io/yaml v1.4.0 // indirect
)
