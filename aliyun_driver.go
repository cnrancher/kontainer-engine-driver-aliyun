package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/rancher/kontainer-engine/drivers/options"
	"github.com/rancher/kontainer-engine/drivers/util"
	"github.com/rancher/kontainer-engine/types"
	"github.com/rancher/rke/log"
	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	raw "google.golang.org/api/container/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	runningStatus        = "RUNNING"
	defaultCredentialEnv = "GOOGLE_APPLICATION_CREDENTIALS"
	none                 = "none"
)

var EnvMutex sync.Mutex

// Driver defines the struct of aliyun driver
type Driver struct {
	driverCapabilities types.Capabilities
}

type state struct {
	// The displayed name of the cluster
	DisplayName string
	// ProjectID is the ID of your project to use when creating a cluster
	ProjectID string
	// The zone to launch the cluster
	Zone string
	// The IP address range of the container pods
	ClusterIpv4Cidr string
	// An optional description of this cluster
	Description string
	// The number of nodes to create in this cluster
	NodeCount int64
	// the kubernetes master version
	MasterVersion string
	// The authentication information for accessing the master
	MasterAuth *raw.MasterAuth
	// the kubernetes node version
	NodeVersion string
	// The name of this cluster
	Name string
	// Parameters used in creating the cluster's nodes
	NodeConfig *raw.NodeConfig
	// The path to the credential file(key.json)
	CredentialPath string
	// The content of the credential
	CredentialContent string
	// Enable alpha feature
	EnableAlphaFeature bool
	// Configuration for the HTTP (L7) load balancing controller addon
	EnableHTTPLoadBalancing *bool
	// Configuration for the horizontal pod autoscaling feature, which increases or decreases the number of replica pods a replication controller has based on the resource usage of the existing pods
	EnableHorizontalPodAutoscaling *bool
	// Configuration for the Kubernetes Dashboard
	EnableKubernetesDashboard bool
	// Configuration for NetworkPolicy
	EnableNetworkPolicyConfig *bool
	// The list of Google Compute Engine locations in which the cluster's nodes should be located
	Locations []string
	// Network
	Network string
	// Sub Network
	SubNetwork string
	// Configuration for LegacyAbac
	LegacyAbac bool
	// NodePool id
	NodePoolID string

	EnableStackdriverLogging    *bool
	EnableStackdriverMonitoring *bool
	MaintenanceWindow           string

	// cluster info
	ClusterInfo types.ClusterInfo
}

func NewDriver() types.Driver {
	driver := &Driver{
		driverCapabilities: types.Capabilities{
			Capabilities: make(map[int64]bool),
		},
	}

	//driver.driverCapabilities.AddCapability(types.GetVersionCapability)
	//driver.driverCapabilities.AddCapability(types.SetVersionCapability)
	driver.driverCapabilities.AddCapability(types.GetClusterSizeCapability)
	driver.driverCapabilities.AddCapability(types.SetClusterSizeCapability)

	return driver
}

// GetDriverCreateOptions implements driver interface
func (d *Driver) GetDriverCreateOptions(ctx context.Context) (*types.DriverFlags, error) {
	driverFlag := types.DriverFlags{
		Options: make(map[string]*types.Flag),
	}
	driverFlag.Options["name"] = &types.Flag{
		Type:  types.StringType,
		Usage: "the internal name of the cluster in Rancher",
	}
	driverFlag.Options["display-name"] = &types.Flag{
		Type:  types.StringType,
		Usage: "the name of the cluster that should be displayed to the user",
	}
	driverFlag.Options["disable-rollback"] = &types.Flag{
		Type:  types.BoolType,
		Usage: "失败是否回滚",
	}
	driverFlag.Options["cluster-type"] = &types.Flag{
		Type:  types.StringType,
		Usage: "集群类型,Kubernetes或ManagedKubernetes",
		Default: &types.Default{
			DefaultString: "ManagedKubernetes",
		},
	}
	driverFlag.Options["timeout-mins"] = &types.Flag{
		Type:  types.IntType,
		Usage: "集群资源栈创建超时时间，以分钟为单位，默认值 60分钟",
	}
	driverFlag.Options["region-id"] = &types.Flag{
		Type:  types.StringType,
		Usage: "集群所在地域ID",
	}
	driverFlag.Options["zone-id"] = &types.Flag{
		Type:  types.StringType,
		Usage: "所属地域的可用区",
	}
	driverFlag.Options["vpc-id"] = &types.Flag{
		Type:  types.StringType,
		Usage: "VPC ID，可空。如果不设置，系统会自动创建VPC，系统创建的VPC网段为192.168.0.0/16。 VpcId 和 vswitchid 只能同时为空或者同时都设置相应的值",
	}
	driverFlag.Options["vswitch-id"] = &types.Flag{
		Type:  types.StringType,
		Usage: "交换机ID，可空。若不设置，系统会自动创建交换机，系统自定创建的交换机网段为 192.168.0.0/16",
	}
	driverFlag.Options["container-cidr"] = &types.Flag{
		Type:  types.StringType,
		Usage: "容器网段，不能和VPC网段冲突。当选择系统自动创建VPC时，默认使用172.16.0.0/16网段",
	}
	driverFlag.Options["service-cidr"] = &types.Flag{
		Type:  types.StringType,
		Usage: "服务网段，不能和VPC网段以及容器网段冲突。当选择系统自动创建VPC时，默认使用172.19.0.0/20网段",
	}
	driverFlag.Options["worker-instance-charge-type"] = &types.Flag{
		Type:  types.StringType,
		Usage: "Worker节点付费类型，可选值为：PrePaid: 预付费; PostPaid: 按量付费",
		Default: &types.Default{
			DefaultString: "PostPaid",
		},
	}
	driverFlag.Options["worker-period-unit"] = &types.Flag{
		Type:  types.StringType,
		Usage: "当指定为PrePaid的时候需要指定周期,Week或Month",
	}
	driverFlag.Options["worker-period"] = &types.Flag{
		Type:  types.IntType,
		Usage: "包年包月时长",
	}
	driverFlag.Options["worker-auto-renew"] = &types.Flag{
		Type:  types.BoolPointerType,
		Usage: "是否开启Worker节点自动续费",
	}
	driverFlag.Options["worker-auto-renew-period"] = &types.Flag{
		Type:  types.IntType,
		Usage: "自动续费周期",
	}
	driverFlag.Options["worker-data-disk"] = &types.Flag{
		Type:  types.BoolType,
		Usage: "是否挂载数据盘",
	}
	driverFlag.Options["worker-data-disk-category"] = &types.Flag{
		Type:  types.StringType,
		Usage: "数据盘类型",
	}

	driverFlag.Options["worker-data-disk-size"] = &types.Flag{
		Type:  types.IntType,
		Usage: "数据盘大小",
	}
	driverFlag.Options["worker-instance-type"] = &types.Flag{
		Type:  types.StringType,
		Usage: "Worker 节点 ECS 规格类型代码",
	}
	driverFlag.Options["worker-system-disk-category"] = &types.Flag{
		Type:  types.StringType,
		Usage: "Worker节点系统盘类型",
	}
	driverFlag.Options["worker-system-disk-size"] = &types.Flag{
		Type:  types.IntType,
		Usage: "Worker节点系统盘大小",
	}
	driverFlag.Options["login-password"] = &types.Flag{
		Type:  types.BoolType,
		Usage: "SSH登录密码。密码规则为8 - 30 个字符，且同时包含三项（大、小写字母，数字和特殊符号）。和key_pair 二选一",
	}
	driverFlag.Options["login_password"] = &types.Flag{
		Type:  types.StringType,
		Usage: "The image to use for the worker nodes",
	}
	driverFlag.Options["key-pair"] = &types.Flag{
		Type:  types.StringType,
		Usage: "keypair名称。与login_password二选一",
	}
	driverFlag.Options["num-of-nodes"] = &types.Flag{
		Type:  types.IntType,
		Usage: "Worker节点数。范围是[0,300]",
	}
	driverFlag.Options["snat-entry"] = &types.Flag{
		Type:  types.StringType,
		Usage: "是否为网络配置SNAT。如果是自动创建VPC必须设置为true。如果使用已有VPC则根据是否具备出网能力来设置",
		Default: &types.Default{
			DefaultBool: true,
		},
	}
	driverFlag.Options["cloud-monitor-flags"] = &types.Flag{
		Type:  types.BoolType,
		Usage: "是否安装云监控插件",
	}
	return &driverFlag, nil
}

// GetDriverUpdateOptions implements driver interface
func (d *Driver) GetDriverUpdateOptions(ctx context.Context) (*types.DriverFlags, error) {
	driverFlag := types.DriverFlags{
		Options: make(map[string]*types.Flag),
	}
	driverFlag.Options["num-of-nodes"] = &types.Flag{
		Type:  types.IntType,
		Usage: "The node number for your cluster to update. 0 means no updates",
	}
	return &driverFlag, nil
}

// SetDriverOptions implements driver interface
func getStateFromOpts(driverOptions *types.DriverOptions) (state, error) {
	d := state{
		NodeConfig: &raw.NodeConfig{
			Labels: map[string]string{},
		},
		ClusterInfo: types.ClusterInfo{
			Metadata: map[string]string{},
		},
	}
	d.Name = options.GetValueFromDriverOptions(driverOptions, types.StringType, "name").(string)
	d.DisplayName = options.GetValueFromDriverOptions(driverOptions, types.StringType, "display-name", "displayName").(string)
	d.ProjectID = options.GetValueFromDriverOptions(driverOptions, types.StringType, "project-id", "projectId").(string)
	d.Zone = options.GetValueFromDriverOptions(driverOptions, types.StringType, "zone").(string)
	d.NodePoolID = options.GetValueFromDriverOptions(driverOptions, types.StringType, "nodePool").(string)
	d.ClusterIpv4Cidr = options.GetValueFromDriverOptions(driverOptions, types.StringType, "cluster-ipv4-cidr", "clusterIpv4Cidr").(string)
	d.Description = options.GetValueFromDriverOptions(driverOptions, types.StringType, "description").(string)
	d.MasterVersion = options.GetValueFromDriverOptions(driverOptions, types.StringType, "master-version", "masterVersion").(string)
	d.NodeVersion = options.GetValueFromDriverOptions(driverOptions, types.StringType, "node-version", "nodeVersion").(string)
	d.NodeConfig.DiskSizeGb = options.GetValueFromDriverOptions(driverOptions, types.IntType, "disk-size-gb", "diskSizeGb").(int64)
	d.NodeConfig.MachineType = options.GetValueFromDriverOptions(driverOptions, types.StringType, "machine-type", "machineType").(string)
	d.CredentialPath = options.GetValueFromDriverOptions(driverOptions, types.StringType, "gke-credential-path").(string)
	d.CredentialContent = options.GetValueFromDriverOptions(driverOptions, types.StringType, "credential").(string)
	d.EnableAlphaFeature = options.GetValueFromDriverOptions(driverOptions, types.BoolType, "enable-alpha-feature", "enableAlphaFeature").(bool)
	d.EnableHorizontalPodAutoscaling, _ = options.GetValueFromDriverOptions(driverOptions, types.BoolPointerType, "enableHorizontalPodAutoscaling").(*bool)
	d.EnableNetworkPolicyConfig, _ = options.GetValueFromDriverOptions(driverOptions, types.BoolPointerType, "enableNetworkPolicyConfig").(*bool)
	d.EnableHTTPLoadBalancing, _ = options.GetValueFromDriverOptions(driverOptions, types.BoolPointerType, "enable-http-load-balancing", "enableHttpLoadBalancing").(*bool)
	d.EnableKubernetesDashboard = options.GetValueFromDriverOptions(driverOptions, types.BoolType, "kubernetes-dashboard", "enableKubernetesDashboard").(bool)
	d.NodeConfig.ImageType = options.GetValueFromDriverOptions(driverOptions, types.StringType, "imageType").(string)
	d.Network = options.GetValueFromDriverOptions(driverOptions, types.StringType, "network").(string)
	d.SubNetwork = options.GetValueFromDriverOptions(driverOptions, types.StringType, "subNetwork").(string)
	d.LegacyAbac = options.GetValueFromDriverOptions(driverOptions, types.BoolType, "legacy-authorization", "enableLegacyAbac").(bool)
	d.Locations = []string{}
	locations := options.GetValueFromDriverOptions(driverOptions, types.StringSliceType, "locations").(*types.StringSlice)
	for _, location := range locations.Value {
		d.Locations = append(d.Locations, location)
	}

	d.NodeCount = options.GetValueFromDriverOptions(driverOptions, types.IntType, "node-count", "nodeCount").(int64)
	labelValues := options.GetValueFromDriverOptions(driverOptions, types.StringSliceType, "labels").(*types.StringSlice)
	for _, part := range labelValues.Value {
		kv := strings.Split(part, "=")
		if len(kv) == 2 {
			d.NodeConfig.Labels[kv[0]] = kv[1]
		}
	}

	d.EnableStackdriverLogging, _ = options.GetValueFromDriverOptions(driverOptions, types.BoolPointerType, "enable-stackdriver-logging", "enableStackdriverLogging").(*bool)
	d.EnableStackdriverMonitoring, _ = options.GetValueFromDriverOptions(driverOptions, types.BoolPointerType, "enable-stackdriver-monitoring", "enableStackdriverMonitoring").(*bool)
	d.MaintenanceWindow = options.GetValueFromDriverOptions(driverOptions, types.StringType, "maintenance-window", "maintenanceWindow").(string)

	return d, d.validate()
}

func (s *state) validate() error {
	if s.ProjectID == "" {
		return fmt.Errorf("project ID is required")
	} else if s.Zone == "" {
		return fmt.Errorf("zone is required")
	} else if s.Name == "" {
		return fmt.Errorf("cluster name is required")
	}
	return nil
}

// Create implements driver interface
func (d *Driver) Create(ctx context.Context, opts *types.DriverOptions, _ *types.ClusterInfo) (*types.ClusterInfo, error) {
	//state, err := getStateFromOpts(opts)
	//if err != nil {
	//	return nil, err
	//}
	//
	//svc, err := d.getServiceClient(ctx, state)
	//if err != nil {
	//	return nil, err
	//}
	//
	//operation, err := svc.Projects.Zones.Clusters.Create(state.ProjectID, state.Zone, d.generateClusterCreateRequest(state)).Context(ctx).Do()
	//if err != nil && !strings.Contains(err.Error(), "alreadyExists") {
	//	return nil, err
	//}
	//
	//if err == nil {
	//	logrus.Debugf("Cluster %s create is called for project %s and zone %s. Status Code %v", state.Name, state.ProjectID, state.Zone, operation.HTTPStatusCode)
	//}
	//
	//if err := d.waitCluster(ctx, svc, &state); err != nil {
	//	return nil, err
	//}
	//
	//info := &types.ClusterInfo{}
	//return info, storeState(info, state)
	return &types.ClusterInfo{}, nil
}

func storeState(info *types.ClusterInfo, state state) error {
	bytes, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if info.Metadata == nil {
		info.Metadata = map[string]string{}
	}
	info.Metadata["state"] = string(bytes)
	info.Metadata["project-id"] = state.ProjectID
	info.Metadata["zone"] = state.Zone
	return nil
}

func getState(info *types.ClusterInfo) (state, error) {
	state := state{}
	// ignore error
	err := json.Unmarshal([]byte(info.Metadata["state"]), &state)
	return state, err
}

// Update implements driver interface
func (d *Driver) Update(ctx context.Context, info *types.ClusterInfo, opts *types.DriverOptions) (*types.ClusterInfo, error) {
	//state, err := getState(info)
	//if err != nil {
	//	return nil, err
	//}
	//
	//newState, err := getStateFromOpts(opts)
	//if err != nil {
	//	return nil, err
	//}
	//
	//svc, err := d.getServiceClient(ctx, state)
	//if err != nil {
	//	return nil, err
	//}
	//
	//if state.NodePoolID == "" {
	//	cluster, err := svc.Projects.Zones.Clusters.Get(state.ProjectID, state.Zone, state.Name).Context(ctx).Do()
	//	if err != nil {
	//		return nil, err
	//	}
	//	state.NodePoolID = cluster.NodePools[0].Name
	//}
	//
	//logrus.Debugf("Updating config. MasterVersion: %s, NodeVersion: %s, NodeCount: %v", state.MasterVersion, state.NodeVersion, state.NodeCount)
	//
	//if newState.MasterVersion != "" {
	//	log.Infof(ctx, "Updating master to %v", newState.MasterVersion)
	//	operation, err := svc.Projects.Zones.Clusters.Update(state.ProjectID, state.Zone, state.Name, &raw.UpdateClusterRequest{
	//		Update: &raw.ClusterUpdate{
	//			DesiredMasterVersion: newState.MasterVersion,
	//		},
	//	}).Context(ctx).Do()
	//	if err != nil {
	//		return nil, err
	//	}
	//	logrus.Debugf("Cluster %s update is called for project %s and zone %s. Status Code %v", state.Name, state.ProjectID, state.Zone, operation.HTTPStatusCode)
	//	if err := d.waitCluster(ctx, svc, &state); err != nil {
	//		return nil, err
	//	}
	//	state.MasterVersion = newState.MasterVersion
	//}
	//
	//if newState.NodeVersion != "" {
	//	log.Infof(ctx, "Updating node version to %v", newState.NodeVersion)
	//	operation, err := svc.Projects.Zones.Clusters.NodePools.Update(state.ProjectID, state.Zone, state.Name, state.NodePoolID, &raw.UpdateNodePoolRequest{
	//		NodeVersion: state.NodeVersion,
	//	}).Context(ctx).Do()
	//	if err != nil {
	//		return nil, err
	//	}
	//	logrus.Debugf("Nodepool %s update is called for project %s, zone %s and cluster %s. Status Code %v", state.NodePoolID, state.ProjectID, state.Zone, state.Name, operation.HTTPStatusCode)
	//	if err := d.waitNodePool(ctx, svc, &state); err != nil {
	//		return nil, err
	//	}
	//	state.NodeVersion = newState.NodeVersion
	//}
	//
	//if newState.NodeCount != 0 {
	//	log.Infof(ctx, "Updating node number to %v", newState.NodeCount)
	//	operation, err := svc.Projects.Zones.Clusters.NodePools.SetSize(state.ProjectID, state.Zone, state.Name, state.NodePoolID, &raw.SetNodePoolSizeRequest{
	//		NodeCount: newState.NodeCount,
	//	}).Context(ctx).Do()
	//	if err != nil {
	//		return nil, err
	//	}
	//	logrus.Debugf("Nodepool %s setSize is called for project %s, zone %s and cluster %s. Status Code %v", state.NodePoolID, state.ProjectID, state.Zone, state.Name, operation.HTTPStatusCode)
	//	if err := d.waitCluster(ctx, svc, &state); err != nil {
	//		return nil, err
	//	}
	//}
	//
	//return info, storeState(info, state)
	return &types.ClusterInfo{}, nil
}

func (d *Driver) generateClusterCreateRequest(state state) *raw.CreateClusterRequest {
	request := raw.CreateClusterRequest{
		Cluster: &raw.Cluster{},
	}
	request.Cluster.Name = state.Name
	request.Cluster.Zone = state.Zone
	request.Cluster.InitialClusterVersion = state.MasterVersion
	request.Cluster.InitialNodeCount = state.NodeCount
	request.Cluster.ClusterIpv4Cidr = state.ClusterIpv4Cidr
	request.Cluster.Description = state.Description
	request.Cluster.EnableKubernetesAlpha = state.EnableAlphaFeature

	disableHTTPLoadBalancing := state.EnableHTTPLoadBalancing != nil && !*state.EnableHTTPLoadBalancing
	disableHorizontalPodAutoscaling := state.EnableHorizontalPodAutoscaling != nil && !*state.EnableHorizontalPodAutoscaling
	disableNetworkPolicyConfig := state.EnableNetworkPolicyConfig != nil && !*state.EnableNetworkPolicyConfig

	request.Cluster.AddonsConfig = &raw.AddonsConfig{
		HttpLoadBalancing:        &raw.HttpLoadBalancing{Disabled: disableHTTPLoadBalancing},
		HorizontalPodAutoscaling: &raw.HorizontalPodAutoscaling{Disabled: disableHorizontalPodAutoscaling},
		KubernetesDashboard:      &raw.KubernetesDashboard{Disabled: !state.EnableKubernetesDashboard},
		NetworkPolicyConfig:      &raw.NetworkPolicyConfig{Disabled: disableNetworkPolicyConfig},
	}
	request.Cluster.Network = state.Network
	request.Cluster.Subnetwork = state.SubNetwork
	request.Cluster.LegacyAbac = &raw.LegacyAbac{
		Enabled: state.LegacyAbac,
	}
	request.Cluster.MasterAuth = &raw.MasterAuth{
		Username: "admin",
	}
	request.Cluster.NodeConfig = state.NodeConfig
	request.Cluster.ResourceLabels = map[string]string{"display-name": strings.ToLower(state.DisplayName)}
	// Stackdriver logging and monitoring default to "on" if no parameter is
	// passed in.  We must explicitly pass "none" if it isn't wanted
	if state.EnableStackdriverLogging != nil && !*state.EnableStackdriverLogging {
		request.Cluster.LoggingService = none
	}
	if state.EnableStackdriverMonitoring != nil && !*state.EnableStackdriverMonitoring {
		request.Cluster.MonitoringService = none
	}
	if state.MaintenanceWindow != "" {
		request.Cluster.MaintenancePolicy = &raw.MaintenancePolicy{
			Window: &raw.MaintenanceWindow{
				DailyMaintenanceWindow: &raw.DailyMaintenanceWindow{
					StartTime: state.MaintenanceWindow,
				},
			},
		}
	}

	return &request
}

func (d *Driver) PostCheck(ctx context.Context, info *types.ClusterInfo) (*types.ClusterInfo, error) {
	//state, err := getState(info)
	//if err != nil {
	//	return nil, err
	//}
	//
	//svc, err := d.getServiceClient(ctx, state)
	//if err != nil {
	//	return nil, err
	//}
	//
	//if err := d.waitCluster(ctx, svc, &state); err != nil {
	//	return nil, err
	//}
	//
	//cluster, err := svc.Projects.Zones.Clusters.Get(state.ProjectID, state.Zone, state.Name).Context(ctx).Do()
	//if err != nil {
	//	return nil, err
	//}
	//
	//info.Endpoint = cluster.Endpoint
	//info.Version = cluster.CurrentMasterVersion
	//info.Username = cluster.MasterAuth.Username
	//info.Password = cluster.MasterAuth.Password
	//info.RootCaCertificate = cluster.MasterAuth.ClusterCaCertificate
	//info.ClientCertificate = cluster.MasterAuth.ClientCertificate
	//info.ClientKey = cluster.MasterAuth.ClientKey
	//info.NodeCount = cluster.CurrentNodeCount
	//info.Metadata["nodePool"] = cluster.NodePools[0].Name
	//serviceAccountToken, err := generateServiceAccountTokenForGke(cluster)
	//if err != nil {
	//	return nil, err
	//}
	//info.ServiceAccountToken = serviceAccountToken
	//return info, nil
	return &types.ClusterInfo{}, nil
}

// Remove implements driver interface
func (d *Driver) Remove(ctx context.Context, info *types.ClusterInfo) error {
	//state, err := getState(info)
	//if err != nil {
	//	return err
	//}
	//
	//svc, err := d.getServiceClient(ctx, state)
	//if err != nil {
	//	return err
	//}
	//
	//logrus.Debugf("Removing cluster %v from project %v, zone %v", state.Name, state.ProjectID, state.Zone)
	//operation, err := svc.Projects.Zones.Clusters.Delete(state.ProjectID, state.Zone, state.Name).Context(ctx).Do()
	//if err != nil && !strings.Contains(err.Error(), "notFound") {
	//	return err
	//} else if err == nil {
	//	logrus.Debugf("Cluster %v delete is called. Status Code %v", state.Name, operation.HTTPStatusCode)
	//} else {
	//	logrus.Debugf("Cluster %s doesn't exist", state.Name)
	//}
	//return nil
	return nil
}

func (d *Driver) getServiceClient(ctx context.Context, state state) (*raw.Service, error) {
	// The google SDK has no sane way to pass in a TokenSource give all the different types (user, service account, etc)
	// So we actually set an environment variable and then unset it
	EnvMutex.Lock()
	locked := true
	setEnv := false
	cleanup := func() {
		if setEnv {
			os.Unsetenv(defaultCredentialEnv)
		}

		if locked {
			EnvMutex.Unlock()
			locked = false
		}
	}
	defer cleanup()

	if state.CredentialPath != "" {
		setEnv = true
		os.Setenv(defaultCredentialEnv, state.CredentialPath)
	}
	if state.CredentialContent != "" {
		file, err := ioutil.TempFile("", "credential-file")
		if err != nil {
			return nil, err
		}
		defer os.Remove(file.Name())
		defer file.Close()

		if _, err := io.Copy(file, strings.NewReader(state.CredentialContent)); err != nil {
			return nil, err
		}

		setEnv = true
		os.Setenv(defaultCredentialEnv, file.Name())
	}

	ts, err := google.DefaultTokenSource(ctx, raw.CloudPlatformScope)
	if err != nil {
		return nil, err
	}

	// Unlocks
	cleanup()

	client := oauth2.NewClient(ctx, ts)
	service, err := raw.New(client)
	if err != nil {
		return nil, err
	}
	return service, nil
}

func generateServiceAccountTokenForGke(cluster *raw.Cluster) (string, error) {
	capem, err := base64.StdEncoding.DecodeString(cluster.MasterAuth.ClusterCaCertificate)
	if err != nil {
		return "", err
	}
	host := cluster.Endpoint
	if !strings.HasPrefix(host, "https://") {
		host = fmt.Sprintf("https://%s", host)
	}
	// in here we have to use http basic auth otherwise we can't get the permission to create cluster role
	config := &rest.Config{
		Host: host,
		TLSClientConfig: rest.TLSClientConfig{
			CAData: capem,
		},
		Username: cluster.MasterAuth.Username,
		Password: cluster.MasterAuth.Password,
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return "", err
	}

	return util.GenerateServiceAccountToken(clientset)
}

func (d *Driver) waitCluster(ctx context.Context, svc *raw.Service, state *state) error {
	lastMsg := ""
	for {
		cluster, err := svc.Projects.Zones.Clusters.Get(state.ProjectID, state.Zone, state.Name).Context(ctx).Do()
		if err != nil {
			return err
		}
		if cluster.Status == runningStatus {
			log.Infof(ctx, "Cluster %v is running", state.Name)
			return nil
		}
		if cluster.Status != lastMsg {
			log.Infof(ctx, "%v cluster %v......", strings.ToLower(cluster.Status), state.Name)
			lastMsg = cluster.Status
		}
		time.Sleep(time.Second * 5)
	}
}

func (d *Driver) waitNodePool(ctx context.Context, svc *raw.Service, state *state) error {
	lastMsg := ""
	for {
		nodepool, err := svc.Projects.Zones.Clusters.NodePools.Get(state.ProjectID, state.Zone, state.Name, state.NodePoolID).Context(ctx).Do()
		if err != nil {
			return err
		}
		if nodepool.Status == runningStatus {
			log.Infof(ctx, "Nodepool %v is running", state.Name)
			return nil
		}
		if nodepool.Status != lastMsg {
			log.Infof(ctx, "%v nodepool %v......", strings.ToLower(nodepool.Status), state.NodePoolID)
			lastMsg = nodepool.Status
		}
		time.Sleep(time.Second * 5)
	}
}

func (d *Driver) getClusterStats(ctx context.Context, info *types.ClusterInfo) (*raw.Cluster, error) {
	state, err := getState(info)

	if err != nil {
		return nil, err
	}

	svc, err := d.getServiceClient(ctx, state)

	if err != nil {
		return nil, err
	}

	cluster, err := svc.Projects.Zones.Clusters.Get(state.ProjectID, state.Zone, state.Name).Context(ctx).Do()

	if err != nil {
		return nil, fmt.Errorf("error getting cluster info: %v", err)
	}

	return cluster, nil
}

func (d *Driver) GetClusterSize(ctx context.Context, info *types.ClusterInfo) (*types.NodeCount, error) {
	//cluster, err := d.getClusterStats(ctx, info)
	//
	//if err != nil {
	//	return nil, err
	//}
	//
	//version := &types.NodeCount{Count: int64(cluster.NodePools[0].InitialNodeCount)}
	//
	//return version, nil
	return &types.NodeCount{Count: 0}, nil
}

func (d *Driver) GetVersion(ctx context.Context, info *types.ClusterInfo) (*types.KubernetesVersion, error) {
	//cluster, err := d.getClusterStats(ctx, info)
	//
	//if err != nil {
	//	return nil, err
	//}
	//
	//version := &types.KubernetesVersion{Version: cluster.CurrentMasterVersion}
	//
	//return version, nil
	return &types.KubernetesVersion{}, nil
}

func (d *Driver) SetClusterSize(ctx context.Context, info *types.ClusterInfo, count *types.NodeCount) error {
	//cluster, err := d.getClusterStats(ctx, info)
	//
	//if err != nil {
	//	return err
	//}
	//
	//state, err := getState(info)
	//
	//if err != nil {
	//	return err
	//}
	//
	//client, err := d.getServiceClient(ctx, state)
	//
	//if err != nil {
	//	return err
	//}
	//
	//logrus.Info("updating cluster size")
	//
	//_, err = client.Projects.Zones.Clusters.NodePools.SetSize(state.ProjectID, state.Zone, cluster.Name, cluster.NodePools[0].Name, &raw.SetNodePoolSizeRequest{
	//	NodeCount: count.Count,
	//}).Context(ctx).Do()
	//
	//if err != nil {
	//	return err
	//}
	//
	//err = d.waitCluster(ctx, client, &state)
	//
	//if err != nil {
	//	return err
	//}
	//
	//logrus.Info("cluster size updated successfully")
	//
	//return nil
	return nil
}

func (d *Driver) SetVersion(ctx context.Context, info *types.ClusterInfo, version *types.KubernetesVersion) error {
	//logrus.Info("updating master version")
	//
	//err := d.updateAndWait(ctx, info, &raw.UpdateClusterRequest{
	//	Update: &raw.ClusterUpdate{
	//		DesiredMasterVersion: version.Version,
	//	}})
	//
	//if err != nil {
	//	return err
	//}
	//
	//logrus.Info("master version updated successfully")
	//logrus.Info("updating node version")
	//
	//err = d.updateAndWait(ctx, info, &raw.UpdateClusterRequest{
	//	Update: &raw.ClusterUpdate{
	//		DesiredNodeVersion: version.Version,
	//	},
	//})
	//
	//if err != nil {
	//	return err
	//}
	//
	//logrus.Info("node version updated successfully")
	//
	//return nil
	return nil
}

func (d *Driver) updateAndWait(ctx context.Context, info *types.ClusterInfo, updateRequest *raw.UpdateClusterRequest) error {
	cluster, err := d.getClusterStats(ctx, info)

	if err != nil {
		return err
	}

	state, err := getState(info)

	if err != nil {
		return err
	}

	client, err := d.getServiceClient(ctx, state)

	if err != nil {
		return err
	}

	_, err = client.Projects.Zones.Clusters.Update(state.ProjectID, state.Zone, cluster.Name, updateRequest).Context(ctx).Do()

	if err != nil {
		return fmt.Errorf("error while updating cluster: %v", err)
	}

	return d.waitCluster(ctx, client, &state)
}

func (d *Driver) GetCapabilities(ctx context.Context) (*types.Capabilities, error) {
	return &d.driverCapabilities, nil
}
