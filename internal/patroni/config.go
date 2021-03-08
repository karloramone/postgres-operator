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

package patroni

import (
	"fmt"
	"path"
	"strings"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/crunchydata/postgres-operator/internal/naming"
	"github.com/crunchydata/postgres-operator/internal/postgres"
	"github.com/crunchydata/postgres-operator/pkg/apis/postgres-operator.crunchydata.com/v1alpha1"
)

const (
	configDirectory  = "/etc/patroni"
	configMapFileKey = "patroni.yaml"
)

const (
	yamlGeneratedWarning = "" +
		"# Generated by postgres-operator. DO NOT EDIT.\n" +
		"# Your changes will not be saved.\n"
)

// quoteShellWord ensures that s is interpreted by a shell as single word.
func quoteShellWord(s string) string {
	// https://www.gnu.org/software/bash/manual/html_node/Quoting.html
	return `'` + strings.ReplaceAll(s, `'`, `'"'"'`) + `'`
}

// clusterYAML returns Patroni settings that apply to the entire cluster.
func clusterYAML(
	cluster *v1alpha1.PostgresCluster, pgUser *v1.Secret,
	pgHBAs postgres.HBAs, pgParameters postgres.Parameters,
) (string, error) {
	root := map[string]interface{}{
		// The cluster identifier. This value cannot change during the cluster's
		// lifetime.
		"scope": naming.PatroniScope(cluster),

		// Use Kubernetes Endpoints for the distributed configuration store (DCS).
		// These values cannot change during the cluster's lifetime.
		//
		// NOTE(cbandy): It *might* be possible to *carefully* change the role and
		// scope labels, but there is no way to reconfigure all instances at once.
		"kubernetes": map[string]interface{}{
			"namespace":     cluster.Namespace,
			"role_label":    naming.LabelRole,
			"scope_label":   naming.LabelPatroni,
			"use_endpoints": true,

			// In addition to "scope_label" above, Patroni will add the following to
			// every object it creates. It will also use these as filters when doing
			// any lookups.
			"labels": map[string]string{
				naming.LabelCluster: cluster.Name,
			},
		},

		"postgresql": map[string]interface{}{
			"authentication": map[string]interface{}{
				// TODO(cbandy): "superuser"
				// FIXME(cbandy): "replication"
				"replication": map[string]interface{}{
					"username": "postgres",
				},
			},

			// TODO(cbandy): "callbacks"

			// When it is enabled, use pgBackRest to create replicas.
			//
			// NOTE(cbandy): Very few environment variables are set. This might belong
			// in the instance configuration because of the data directory.
			// NOTE(cbandy): Is there any chance a user might want to specify their own
			// method? This is a list and cannot be merged.
			"create_replica_methods": []string{},

			// Custom configuration "must exist on all cluster nodes".
			//
			// TODO(cbandy): I imagine we will always set this to a file we own. At
			// the very least, it will start with an "include_dir" directive.
			// - https://www.postgresql.org/docs/current/config-setting.html#CONFIG-INCLUDES
			//"custom_conf": nil,

			// TODO(cbandy): Should "parameters", "pg_hba", and "pg_ident" be set in
			// DCS? If so, are they are automatically regenerated and reloaded?
		},

		// NOTE(cbandy): Every Patroni instance is a client of every other Patroni
		// instance. TLS and/or authentication settings need to be applied consistently
		// across the entire cluster.

		"restapi": map[string]interface{}{
			// Use TLS to encrypt traffic and verify clients.
			// NOTE(cbandy): The path package always uses slash separators.
			"cafile":   path.Join(configDirectory, certAuthorityConfigPath),
			"certfile": path.Join(configDirectory, certServerConfigPath),

			// The private key is bundled into "restapi.certfile".
			"keyfile": nil,

			// Require clients to present a certificate verified by "restapi.cafile"
			// when calling "unsafe" API endpoints.
			// - https://github.com/zalando/patroni/blob/v2.0.1/docs/security.rst#protecting-the-rest-api
			//
			// NOTE(cbandy): We'd prefer "required" here, but Kubernetes HTTPS probes
			// offer no way to present client certificates. Perhaps Patroni could change
			// to relax the requirement on *just* liveness and readiness?
			// - https://issue.k8s.io/92647
			"verify_client": "optional",

			// TODO(cbandy): The next release of Patroni will allow more control over
			// the TLS protocols/ciphers.
			// Maybe "ciphers": "EECDH+AESGCM+FIPS:EDH+AESGCM+FIPS". Maybe add ":!DHE".
			// - https://github.com/zalando/patroni/commit/ba4ab58d4069ee30
		},

		"ctl": map[string]interface{}{
			// Use TLS to verify the server and present a client certificate.
			// NOTE(cbandy): The path package always uses slash separators.
			"cacert":   path.Join(configDirectory, certAuthorityConfigPath),
			"certfile": path.Join(configDirectory, certServerConfigPath),

			// The private key is bundled into "ctl.certfile".
			"keyfile": nil,

			// Always verify the server certificate against "ctl.cacert".
			"insecure": false,
		},

		"watchdog": map[string]interface{}{
			// Disable leader watchdog device. Kubernetes' liveness probe is a less
			// flexible approximation.
			"mode": "off",
		},
	}

	if cluster.Status.Patroni == nil || cluster.Status.Patroni.SystemIdentifier == "" {
		// Patroni has not yet bootstrapped. Populate the "bootstrap.dcs" field to
		// facilitate it. When Patroni is already bootstrapped, this field is ignored.

		// Deserialize the schemaless field. There will be no error because the
		// Kubernetes API has already ensured it is a JSON object.
		configuration := make(map[string]interface{})
		if cluster.Spec.Patroni != nil {
			_ = yaml.Unmarshal(
				cluster.Spec.Patroni.DynamicConfiguration.Raw, &configuration,
			)
		}

		// TODO(cbandy): This belongs somewhere else; postgres package?
		sql := `
CREATE ROLE :"user" LOGIN PASSWORD :'password';
CREATE DATABASE :"dbname";
GRANT ALL PRIVILEGES ON DATABASE :"dbname" TO :"user";
`

		root["bootstrap"] = map[string]interface{}{
			"dcs": DynamicConfiguration(cluster, configuration, pgHBAs, pgParameters),

			// Pass generated values as variables to psql and use --file to
			// interpolate them safely in the initialization SQL.
			// - https://www.postgresql.org/docs/current/app-psql.html#APP-PSQL-INTERPOLATION
			"post_bootstrap": "bash -c " + quoteShellWord("psql"+
				" --set=ON_ERROR_STOP=1"+
				" --set=dbname="+quoteShellWord(string(pgUser.Data["dbname"]))+
				" --set=password="+quoteShellWord(string(pgUser.Data["verifier"]))+
				" --set=user="+quoteShellWord(string(pgUser.Data["user"]))+
				" --file=- <<< "+quoteShellWord(sql),
			),

			// Missing here is "users" which runs *after* "post_boostrap". It is
			// not possible to use roles created by the former in the latter.
			// - https://github.com/zalando/patroni/issues/667
		}
	}

	b, err := yaml.Marshal(root)
	return string(append([]byte(yamlGeneratedWarning), b...)), err
}

// DynamicConfiguration combines configuration with some PostgreSQL settings
// and returns a value that can be marshaled to JSON.
func DynamicConfiguration(
	cluster *v1alpha1.PostgresCluster,
	configuration map[string]interface{},
	pgHBAs postgres.HBAs, pgParameters postgres.Parameters,
) map[string]interface{} {
	// Copy the entire configuration before making any changes.
	root := make(map[string]interface{}, len(configuration))
	for k, v := range configuration {
		root[k] = v
	}

	root["ttl"] = *cluster.Spec.Patroni.LeaderLeaseDurationSeconds
	root["loop_wait"] = *cluster.Spec.Patroni.SyncPeriodSeconds

	// Copy the "postgresql" section before making any changes.
	postgresql := make(map[string]interface{})
	if section, ok := root["postgresql"].(map[string]interface{}); ok {
		for k, v := range section {
			postgresql[k] = v
		}
	}
	root["postgresql"] = postgresql

	// Copy the "postgresql.parameters" section over any defaults.
	parameters := make(map[string]interface{})
	if pgParameters.Default != nil {
		for k, v := range pgParameters.Default.AsMap() {
			parameters[k] = v
		}
	}
	if section, ok := postgresql["parameters"].(map[string]interface{}); ok {
		for k, v := range section {
			parameters[k] = v
		}
	}
	// Override the above with mandatory parameters.
	if pgParameters.Mandatory != nil {
		for k, v := range pgParameters.Mandatory.AsMap() {
			parameters[k] = v
		}
	}
	postgresql["parameters"] = parameters

	// Copy the "postgresql.pg_hba" section after any mandatory values.
	hba := make([]string, len(pgHBAs.Mandatory))
	for i := range pgHBAs.Mandatory {
		hba[i] = pgHBAs.Mandatory[i].String()
	}
	if section, ok := postgresql["pg_hba"].([]string); ok {
		hba = append(hba, section...)
	}
	// When the section is missing or empty, include the recommended defaults.
	if len(hba) == len(pgHBAs.Mandatory) {
		for i := range pgHBAs.Default {
			hba = append(hba, pgHBAs.Default[i].String())
		}
	}
	postgresql["pg_hba"] = hba

	// TODO(cbandy): explain this.
	postgresql["use_pg_rewind"] = true

	return root
}

// instanceEnvironment returns the environment variables needed by Patroni's
// instance container.
func instanceEnvironment(
	cluster *v1alpha1.PostgresCluster,
	clusterPodService *v1.Service,
	leaderService *v1.Service,
	podContainers []v1.Container,
) []v1.EnvVar {
	var (
		patroniPort  = *cluster.Spec.Patroni.Port
		postgresPort = *cluster.Spec.Port
		podSubdomain = clusterPodService.Name
	)

	// Gather Endpoint ports for any Container ports that match the leader
	// Service definition.
	ports := []v1.EndpointPort{}
	for _, sp := range leaderService.Spec.Ports {
		for i := range podContainers {
			for _, cp := range podContainers[i].Ports {
				if sp.TargetPort.StrVal == cp.Name {
					ports = append(ports, v1.EndpointPort{
						Name:     sp.Name,
						Port:     cp.ContainerPort,
						Protocol: cp.Protocol,
					})
				}
			}
		}
	}
	portsYAML, _ := yaml.Marshal(ports)

	variables := []v1.EnvVar{
		// Set "name" to the v1.Pod's name. Required when using Kubernetes for DCS.
		// Patroni must be restarted when changing this value.
		{
			Name: "PATRONI_NAME",
			ValueFrom: &v1.EnvVarSource{FieldRef: &v1.ObjectFieldSelector{
				APIVersion: "v1",
				FieldPath:  "metadata.name",
			}},
		},

		// Set "kubernetes.pod_ip" to the v1.Pod's primary IP address.
		// Patroni must be restarted when changing this value.
		{
			Name: "PATRONI_KUBERNETES_POD_IP",
			ValueFrom: &v1.EnvVarSource{FieldRef: &v1.ObjectFieldSelector{
				APIVersion: "v1",
				FieldPath:  "status.podIP",
			}},
		},

		// When using Endpoints for DCS, Patroni needs to replicate the leader
		// ServicePort definitions. Set "kubernetes.ports" to the YAML of this
		// Pod's equivalent EndpointPort definitions.
		//
		// This is connascent with PATRONI_POSTGRESQL_CONNECT_ADDRESS below.
		// Patroni must be restarted when changing this value.
		{
			Name:  "PATRONI_KUBERNETES_PORTS",
			Value: string(portsYAML),
		},

		// Set "postgresql.connect_address" using the Pod's stable DNS name.
		// PostgreSQL must be restarted when changing this value.
		{
			Name:  "PATRONI_POSTGRESQL_CONNECT_ADDRESS",
			Value: fmt.Sprintf("%s.%s:%d", "$(PATRONI_NAME)", podSubdomain, postgresPort),
		},

		// Set "postgresql.listen" using the special address "*" to mean all TCP
		// interfaces. When connecting locally over TCP, Patroni will use "localhost".
		//
		// This is connascent with PATRONI_POSTGRESQL_CONNECT_ADDRESS above.
		// PostgreSQL must be restarted when changing this value.
		{
			Name:  "PATRONI_POSTGRESQL_LISTEN",
			Value: fmt.Sprintf("*:%d", postgresPort),
		},

		// Set "restapi.connect_address" using the Pod's stable DNS name.
		// Patroni must be reloaded when changing this value.
		{
			Name:  "PATRONI_RESTAPI_CONNECT_ADDRESS",
			Value: fmt.Sprintf("%s.%s:%d", "$(PATRONI_NAME)", podSubdomain, patroniPort),
		},

		// Set "restapi.listen" using the special address "*" to mean all TCP interfaces.
		// This is connascent with PATRONI_RESTAPI_CONNECT_ADDRESS above.
		// Patroni must be reloaded when changing this value.
		{
			Name:  "PATRONI_RESTAPI_LISTEN",
			Value: fmt.Sprintf("*:%d", patroniPort),
		},

		// The Patroni client `patronictl` looks here for its configuration file(s).
		{
			Name:  "PATRONICTL_CONFIG_FILE",
			Value: configDirectory,
		},
	}

	return variables
}

// instanceConfigFiles returns projections of Patroni's configuration files
// to include in the instance configuration volume.
func instanceConfigFiles(cluster, instance *v1.ConfigMap) []v1.VolumeProjection {
	return []v1.VolumeProjection{
		{
			ConfigMap: &v1.ConfigMapProjection{
				LocalObjectReference: v1.LocalObjectReference{
					Name: cluster.Name,
				},
				Items: []v1.KeyToPath{{
					Key:  configMapFileKey,
					Path: "~postgres-operator_cluster.yaml",
				}},
			},
		},
		{
			ConfigMap: &v1.ConfigMapProjection{
				LocalObjectReference: v1.LocalObjectReference{
					Name: instance.Name,
				},
				Items: []v1.KeyToPath{{
					Key:  configMapFileKey,
					Path: "~postgres-operator_instance.yaml",
				}},
			},
		},
	}
}

// instanceYAML returns Patroni settings that apply to instance.
func instanceYAML(_ *v1alpha1.PostgresCluster, _ metav1.Object) (string, error) {
	root := map[string]interface{}{
		// Missing here is "name" which cannot be known until the instance Pod is
		// created. That value should be injected using the downward API and the
		// PATRONI_NAME environment variable.

		"kubernetes": map[string]interface{}{
			// Missing here is "pod_ip" which cannot be known until the instance Pod is
			// created. That value should be injected using the downward API and the
			// PATRONI_KUBERNETES_POD_IP environment variable.

			// Missing here is "ports" which is is connascent with "postgresql.connect_address".
			// See the PATRONI_KUBERNETES_PORTS env variable.
		},

		"postgresql": map[string]interface{}{
			// TODO(cbandy): "bin_dir"
			// TODO(cbandy): "config_dir" so that users cannot override.

			// Missing here is "connect_address" which cannot be known until the
			// instance Pod is created. That value should be injected using the downward
			// API and the PATRONI_POSTGRESQL_CONNECT_ADDRESS environment variable.

			// FIXME(cbandy): "data_dir"
			"data_dir": "/tmp/data_dir",

			// Missing here is "listen" which is connascent with "connect_address".
			// See the PATRONI_POSTGRESQL_LISTEN environment variable.

			// TODO(cbandy): "pgpass"

			// Prefer to use UNIX domain sockets for local connections. If the PostgreSQL
			// parameter "unix_socket_directories" is set, Patroni will connect using one
			// of those directories. Otherwise, it will use the client (libpq) default.
			"use_unix_socket": true,
		},

		"restapi": map[string]interface{}{
			// Missing here is "connect_address" which cannot be known until the
			// instance Pod is created. That value should be injected using the downward
			// API and the PATRONI_RESTAPI_CONNECT_ADDRESS environment variable.

			// Missing here is "listen" which is connascent with "connect_address".
			// See the PATRONI_RESTAPI_LISTEN environment variable.
		},

		"tags": map[string]interface{}{
			// TODO(cbandy): "nofailover"
			// TODO(cbandy): "nosync"
		},
	}

	b, err := yaml.Marshal(root)
	return string(append([]byte(yamlGeneratedWarning), b...)), err
}

// probeTiming returns a Probe with thresholds and timeouts set according to spec.
func probeTiming(spec *v1alpha1.PatroniSpec) *v1.Probe {
	// "Probes should be configured in such a way that they start failing about
	// time when the leader key is expiring."
	// - https://github.com/zalando/patroni/blob/v2.0.1/docs/rest_api.rst
	// - https://github.com/zalando/patroni/blob/v2.0.1/docs/watchdog.rst

	// TODO(cbandy): When the probe times out, failure triggers at
	// (FailureThreshold × PeriodSeconds + TimeoutSeconds)
	probe := v1.Probe{
		TimeoutSeconds:   *spec.SyncPeriodSeconds / 2,
		PeriodSeconds:    *spec.SyncPeriodSeconds,
		SuccessThreshold: 1,
		FailureThreshold: *spec.LeaderLeaseDurationSeconds / *spec.SyncPeriodSeconds,
	}

	if probe.TimeoutSeconds < 1 {
		probe.TimeoutSeconds = 1
	}
	if probe.FailureThreshold < 1 {
		probe.FailureThreshold = 1
	}

	return &probe
}
