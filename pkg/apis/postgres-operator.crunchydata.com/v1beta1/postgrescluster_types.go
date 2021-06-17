/*
 Copyright 2021 Crunchy Data Solutions, Inc.
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

package v1beta1

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DedicatedRepo defines a pgBackRest dedicated repository host
type DedicatedRepo struct {

	// Resource requirements for the dedicated repository host
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// Scheduling constraints of the Dedicated repo host pod.
	// Changing this value causes repo host to restart.
	// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// Tolerations of a PgBackRest repo host pod. Changing this value causes a restart.
	// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
}

// PostgresClusterSpec defines the desired state of PostgresCluster
type PostgresClusterSpec struct {
	// +optional
	Metadata *Metadata `json:"metadata,omitempty"`

	// Specifies a data source for bootstrapping the PostgreSQL cluster.
	// +optional
	DataSource *DataSource `json:"dataSource,omitempty"`

	// PostgreSQL archive configuration
	// +kubebuilder:validation:Required
	Archive Archive `json:"archive"`

	// The secret containing the Certificates and Keys to encrypt PostgreSQL
	// traffic will need to contain the server TLS certificate, TLS key and the
	// Certificate Authority certificate with the data keys set to tls.crt,
	// tls.key and ca.crt, respectively. It will then be mounted as a volume
	// projection to the '/pgconf/tls' directory. For more information on
	// Kubernetes secret projections, please see
	// https://k8s.io/docs/concepts/configuration/secret/#projection-of-secret-keys-to-specific-paths
	// NOTE: If CustomTLSSecret is provided, CustomReplicationClientTLSSecret
	// MUST be provided and the ca.crt provided must be the same.
	// +optional
	CustomTLSSecret *corev1.SecretProjection `json:"customTLSSecret,omitempty"`

	// The secret containing the replication client certificates and keys for
	// secure connections to the PostgreSQL server. It will need to contain the
	// client TLS certificate, TLS key and the Certificate Authority certificate
	// with the data keys set to tls.crt, tls.key and ca.crt, respectively.
	// NOTE: If CustomReplicationClientTLSSecret is provided, CustomTLSSecret
	// MUST be provided and the ca.crt provided must be the same.
	// +optional
	CustomReplicationClientTLSSecret *corev1.SecretProjection `json:"customReplicationTLSSecret,omitempty"`

	// The image name to use for PostgreSQL containers
	// +kubebuilder:validation:Required
	Image string `json:"image"`

	// The image pull secrets used to pull from a private registry
	// Changing this value causes all running pods to restart.
	// https://k8s.io/docs/tasks/configure-pod-container/pull-image-private-registry/
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// +listType=map
	// +listMapKey=name
	InstanceSets []PostgresInstanceSetSpec `json:"instances"`

	// Whether or not the PostgreSQL cluster is being deployed to an OpenShift envioronment
	// +optional
	OpenShift *bool `json:"openshift,omitempty"`

	// +optional
	Patroni *PatroniSpec `json:"patroni,omitempty"`

	// The port on which PostgreSQL should listen.
	// +optional
	// +kubebuilder:default=5432
	// +kubebuilder:validation:Minimum=1024
	Port *int32 `json:"port,omitempty"`

	// The major version of PostgreSQL installed in the PostgreSQL container
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=10
	// +kubebuilder:validation:Maximum=13
	PostgresVersion int `json:"postgresVersion"`

	// The specification of a proxy that connects to PostgreSQL.
	// +optional
	Proxy *PostgresProxySpec `json:"proxy,omitempty"`

	// Whether or not the PostgreSQL cluster should be stopped.
	// When this is true, workloads are scaled to zero and CronJobs
	// are suspended.
	// Other resources, such as Services and Volumes, remain in place.
	// +optional
	Shutdown *bool `json:"shutdown,omitempty"`
}

// DataSource defines the source of the PostgreSQL data directory for a new PostgresCluster.
type DataSource struct {
	// Defines a pgBackRest data source that can be used to pre-populate the PostgreSQL data
	// directory for a new PostgreSQL cluster using a pgBackRest restore.
	// +optional
	PostgresCluster *PostgresClusterDataSource `json:"postgresCluster,omitempty"`
}

// PostgresClusterDataSource defines a data source for bootstrapping PostgreSQL clusters using a
// an existing PostgresCluster.
type PostgresClusterDataSource struct {

	// The name of an existing PostgresCluster to use as the datasource for the new PostgresCluster.
	// PostgresCluster.
	// +kubebuilder:validation:Required
	ClusterName string `json:"clusterName,omitempty"`

	// The name of the pgBackRest repo within the source PostgresCluster that contains the backups
	// that should be utilized to perform a pgBackRest restore when initializing the data source
	// for the new PostgresCluster.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=^repo[1-4]
	RepoName string `json:"repoName"`

	// Command line options to include when running the pgBackRest restore command.
	// https://pgbackrest.org/command.html#command-restore
	// +optional
	Options []string `json:"options,omitempty"`

	// Resource requirements for the pgBackRest restore Job.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

func (s *PostgresClusterSpec) Default() {
	for i := range s.InstanceSets {
		s.InstanceSets[i].Default(i)
	}

	if s.Patroni == nil {
		s.Patroni = new(PatroniSpec)
	}
	s.Patroni.Default()

	if s.Port == nil {
		s.Port = new(int32)
		*s.Port = 5432
	}

	if s.Proxy != nil {
		s.Proxy.Default()
	}
}

// Archive defines a PostgreSQL archive configuration
type Archive struct {

	// pgBackRest archive configuration
	// +kubebuilder:validation:Required
	PGBackRest PGBackRestArchive `json:"pgbackrest"`
}

// PostgresClusterStatus defines the observed state of PostgresCluster
type PostgresClusterStatus struct {

	// Current state of PostgreSQL instances.
	// +listType=map
	// +listMapKey=name
	// +optional
	InstanceSets []PostgresInstanceSetStatus `json:"instances,omitempty"`

	// +optional
	Patroni *PatroniStatus `json:"patroni,omitempty"`

	// Status information for pgBackRest
	// +optional
	PGBackRest *PGBackRestStatus `json:"pgbackrest,omitempty"`

	// Current state of the PostgreSQL proxy.
	// +optional
	Proxy PostgresProxyStatus `json:"proxy,omitempty"`

	// The previous leader instance to start first when a cluster is
	// restarted after a shutdown
	// +optional
	StartupInstance string `json:"startupInstance,omitempty"`

	// observedGeneration represents the .metadata.generation on which the status was based.
	// +optional
	// +kubebuilder:validation:Minimum=0
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions represent the observations of postgrescluster's current state.
	// Known .status.conditions.type are: "PersistentVolumeResizing",
	// "ProxyAvailable"
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// PostgresClusterStatus condition types.
const (
	PersistentVolumeResizing = "PersistentVolumeResizing"
	ProxyAvailable           = "ProxyAvailable"
)

type PostgresInstanceSetSpec struct {
	// +optional
	Metadata *Metadata `json:"metadata,omitempty"`

	// +optional
	// +kubebuilder:default=""
	Name string `json:"name"`

	// Scheduling constraints of a Instance pod. Changing this value causes
	// Instance to restart.
	// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// Defines a PersistentVolumeClaim for PostgreSQL data.
	// More info: https://kubernetes.io/docs/concepts/storage/persistent-volumes
	// +kubebuilder:validation:Required
	DataVolumeClaimSpec corev1.PersistentVolumeClaimSpec `json:"dataVolumeClaimSpec"`

	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	Replicas *int32 `json:"replicas,omitempty"`

	// Compute resources of a PostgreSQL container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// Tolerations of a PostgreSQL pod. Changing this value causes PostgreSQL to restart.
	// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// Defines a separate PersistentVolumeClaim for PostgreSQL's write-ahead log.
	// More info: https://www.postgresql.org/docs/current/wal.html
	// +optional
	WALVolumeClaimSpec *corev1.PersistentVolumeClaimSpec `json:"walVolumeClaimSpec,omitempty"`
}

func (s *PostgresInstanceSetSpec) Default(i int) {
	if s.Name == "" {
		s.Name = fmt.Sprintf("%02d", i)
	}
	if s.Replicas == nil {
		s.Replicas = new(int32)
		*s.Replicas = 1
	}
}

type PostgresInstanceSetStatus struct {
	Name string `json:"name"`

	// Total number of ready pods.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// Total number of non-terminated pods.
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// Total number of non-terminated pods that have the desired specification.
	// +optional
	UpdatedReplicas int32 `json:"updatedReplicas,omitempty"`
}

// PostgresProxySpec is a union of the supported PostgreSQL proxies.
type PostgresProxySpec struct {

	// Defines a PgBouncer proxy and connection pooler.
	PGBouncer *PGBouncerPodSpec `json:"pgBouncer"`
}

func (s *PostgresProxySpec) Default() {
	if s.PGBouncer != nil {
		s.PGBouncer.Default()
	}
}

type PostgresProxyStatus struct {
	PGBouncer PGBouncerPodStatus `json:"pgBouncer,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// PostgresCluster is the Schema for the postgresclusters API
type PostgresCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// NOTE(cbandy): Every PostgresCluster needs a Spec, but it is optional here
	// so ObjectMeta can be managed independently.

	Spec   PostgresClusterSpec   `json:"spec,omitempty"`
	Status PostgresClusterStatus `json:"status,omitempty"`
}

// Default implements "sigs.k8s.io/controller-runtime/pkg/webhook.Defaulter" so
// a webhook can be registered for the type.
// - https://book.kubebuilder.io/reference/webhook-overview.html
func (c *PostgresCluster) Default() {
	if len(c.APIVersion) == 0 {
		c.APIVersion = GroupVersion.String()
	}
	if len(c.Kind) == 0 {
		c.Kind = "PostgresCluster"
	}
	c.Spec.Default()
}

// +kubebuilder:object:root=true

// PostgresClusterList contains a list of PostgresCluster
type PostgresClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PostgresCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PostgresCluster{}, &PostgresClusterList{})
}

// Metadata contains metadata for PostgresCluster resources
type Metadata struct {
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// GetLabelsOrNil gets labels from a Metadata pointer, if Metadata
// hasn't been set return nil
func (meta *Metadata) GetLabelsOrNil() map[string]string {
	if meta == nil {
		return nil
	}
	return meta.Labels
}

// GetAnnotationsOrNil gets annotations from a Metadata pointer, if Metadata
// hasn't been set return nil
func (meta *Metadata) GetAnnotationsOrNil() map[string]string {
	if meta == nil {
		return nil
	}
	return meta.Annotations
}
