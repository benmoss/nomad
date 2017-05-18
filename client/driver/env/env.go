package env

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/hashicorp/nomad/helper"
	hargs "github.com/hashicorp/nomad/helper/args"
	"github.com/hashicorp/nomad/nomad/structs"
)

// A set of environment variables that are exported by each driver.
const (
	// AllocDir is the environment variable with the path to the alloc directory
	// that is shared across tasks within a task group.
	AllocDir = "NOMAD_ALLOC_DIR"

	// TaskLocalDir is the environment variable with the path to the tasks local
	// directory where it can store data that is persisted to the alloc is
	// removed.
	TaskLocalDir = "NOMAD_TASK_DIR"

	// SecretsDir is the environment variable with the path to the tasks secret
	// directory where it can store sensitive data.
	SecretsDir = "NOMAD_SECRETS_DIR"

	// MemLimit is the environment variable with the tasks memory limit in MBs.
	MemLimit = "NOMAD_MEMORY_LIMIT"

	// CpuLimit is the environment variable with the tasks CPU limit in MHz.
	CpuLimit = "NOMAD_CPU_LIMIT"

	// AllocID is the environment variable for passing the allocation ID.
	AllocID = "NOMAD_ALLOC_ID"

	// AllocName is the environment variable for passing the allocation name.
	AllocName = "NOMAD_ALLOC_NAME"

	// TaskName is the environment variable for passing the task name.
	TaskName = "NOMAD_TASK_NAME"

	// JobName is the environment variable for passing the job name.
	JobName = "NOMAD_JOB_NAME"

	// AllocIndex is the environment variable for passing the allocation index.
	AllocIndex = "NOMAD_ALLOC_INDEX"

	// Datacenter is the environment variable for passing the datacenter in which the alloc is running.
	Datacenter = "NOMAD_DC"

	// Region is the environment variable for passing the region in which the alloc is running.
	Region = "NOMAD_REGION"

	// AddrPrefix is the prefix for passing both dynamic and static port
	// allocations to tasks.
	// E.g $NOMAD_ADDR_http=127.0.0.1:80
	AddrPrefix = "NOMAD_ADDR_"

	// IpPrefix is the prefix for passing the IP of a port allocation to a task.
	IpPrefix = "NOMAD_IP_"

	// PortPrefix is the prefix for passing the port allocation to a task.
	PortPrefix = "NOMAD_PORT_"

	// HostPortPrefix is the prefix for passing the host port when a portmap is
	// specified.
	HostPortPrefix = "NOMAD_HOST_PORT_"

	// MetaPrefix is the prefix for passing task meta data.
	MetaPrefix = "NOMAD_META_"

	// VaultToken is the environment variable for passing the Vault token
	VaultToken = "VAULT_TOKEN"
)

// The node values that can be interpreted.
const (
	nodeIdKey     = "node.unique.id"
	nodeDcKey     = "node.datacenter"
	nodeRegionKey = "node.region"
	nodeNameKey   = "node.unique.name"
	nodeClassKey  = "node.class"

	// Prefixes used for lookups.
	nodeAttributePrefix = "attr."
	nodeMetaPrefix      = "meta."
)

// TaskEnv is a task's environment as well as node attribute's for
// interpolation.
type TaskEnv struct {
	// NodeAttrs is the map of node attributes for interpolation
	NodeAttrs map[string]string

	// EnvMap is the map of environment variables
	EnvMap map[string]string

	// envList is a memoized list created by List()
	envList []string
}

// NewTaskEnv creates a new task environment with the given environment and
// node attribute maps.
func NewTaskEnv(env, node map[string]string) *TaskEnv {
	return &TaskEnv{
		NodeAttrs: node,
		EnvMap:    env,
	}
}

// List returns the task's environment as a slice of NAME=value pair strings.
func (t *TaskEnv) List() []string {
	if t.envList != nil {
		return t.envList
	}

	env := []string{}
	for k, v := range t.EnvMap {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	return env
}

// Map of the task's environment variables.
func (t *TaskEnv) Map() map[string]string {
	m := make(map[string]string, len(t.EnvMap))
	for k, v := range t.EnvMap {
		m[k] = v
	}

	return m
}

// All of the task's environment variables and the node's attributes in a
// single map.
func (t *TaskEnv) All() map[string]string {
	m := make(map[string]string, len(t.EnvMap)+len(t.NodeAttrs))
	for k, v := range t.EnvMap {
		m[k] = v
	}
	for k, v := range t.NodeAttrs {
		m[k] = v
	}

	return m
}

// ParseAndReplace takes the user supplied args replaces any instance of an
// environment variable or Nomad variable in the args with the actual value.
func (t *TaskEnv) ParseAndReplace(args []string) []string {
	replaced := make([]string, len(args))
	for i, arg := range args {
		replaced[i] = hargs.ReplaceEnv(arg, t.EnvMap, t.NodeAttrs)
	}

	return replaced
}

// ReplaceEnv takes an arg and replaces all occurrences of environment variables
// and Nomad variables.  If the variable is found in the passed map it is
// replaced, otherwise the original string is returned.
func (t *TaskEnv) ReplaceEnv(arg string) string {
	return hargs.ReplaceEnv(arg, t.EnvMap, t.NodeAttrs)
}

// Builder is used to build task environment's and is safe for concurrent use.
type Builder struct {
	// envvars are custom set environment variables
	envvars map[string]string

	// templateEnv are env vars set from templates
	templateEnv map[string]string

	// hostEnv are environment variables filtered from the host
	hostEnv map[string]string

	// nodeAttrs are Node attributes and metadata
	nodeAttrs map[string]string

	// taskMeta are the meta attributes on the task
	taskMeta map[string]string

	// allocDir from task's perspective; eg /alloc
	allocDir string

	// localDir from task's perspective; eg /local
	localDir string

	// secrestsDir from task's perspective; eg /secrets
	secretsDir string

	cpuLimit         int
	memLimit         int
	taskName         string
	allocIndex       int
	datacenter       string
	region           string
	allocId          string
	allocName        string
	portMap          map[string]string
	vaultToken       string
	injectVaultToken bool
	jobName          string

	// otherPorts for tasks in the same alloc
	otherPorts map[string]string

	// networks related environment variables
	networks map[string]string

	mu *sync.RWMutex
}

// NewBuilder creates a new task environment builder.
func NewBuilder(node *structs.Node, taskName string) *Builder {
	b := &Builder{
		taskName:  taskName,
		envvars:   make(map[string]string),
		nodeAttrs: make(map[string]string),
		mu:        &sync.RWMutex{},
	}
	return b.setNode(node)
}

// Build must be called after all the tasks environment values have been set.
func (b *Builder) Build() *TaskEnv {
	nodeAttrs := make(map[string]string)
	envMap := make(map[string]string)

	b.mu.RLock()
	defer b.mu.RUnlock()

	// Add the directories
	if b.allocDir != "" {
		envMap[AllocDir] = b.allocDir
	}
	if b.localDir != "" {
		envMap[TaskLocalDir] = b.localDir
	}
	if b.secretsDir != "" {
		envMap[SecretsDir] = b.secretsDir
	}

	// Add the resource limits
	if b.memLimit != 0 {
		envMap[MemLimit] = strconv.Itoa(b.memLimit)
	}
	if b.cpuLimit != 0 {
		envMap[CpuLimit] = strconv.Itoa(b.cpuLimit)
	}

	// Add the task metadata
	if b.allocId != "" {
		envMap[AllocID] = b.allocId
	}
	if b.allocName != "" {
		envMap[AllocName] = b.allocName
	}
	if b.allocIndex != -1 {
		envMap[AllocIndex] = strconv.Itoa(b.allocIndex)
	}
	if b.taskName != "" {
		envMap[TaskName] = b.taskName
	}
	if b.jobName != "" {
		envMap[JobName] = b.jobName
	}
	if b.datacenter != "" {
		envMap[Datacenter] = b.datacenter
	}
	if b.region != "" {
		envMap[Region] = b.region

		// Copy region over to node attrs
		nodeAttrs[nodeRegionKey] = b.region
	}

	// Build the addr of the other tasks
	for k, v := range b.otherPorts {
		envMap[k] = v
	}

	// Build the Vault Token
	if b.injectVaultToken && b.vaultToken != "" {
		envMap[VaultToken] = b.vaultToken
	}

	// Copy node attributes
	for k, v := range b.nodeAttrs {
		nodeAttrs[k] = v
	}

	// Interpolate and add environment variables
	for k, v := range b.hostEnv {
		envMap[k] = hargs.ReplaceEnv(v, nodeAttrs, envMap)
	}

	// Copy task env vars second as they override host env vars
	for k, v := range b.envvars {
		envMap[k] = v
	}

	// Copy template env vars third as they override task env vars
	for k, v := range b.templateEnv {
		envMap[k] = v
	}

	// Clean keys (see #2405)
	cleanedEnv := make(map[string]string, len(envMap))
	for k, v := range envMap {
		cleanedK := helper.CleanEnvVar(k, '_')
		cleanedEnv[cleanedK] = v
	}

	return NewTaskEnv(cleanedEnv, nodeAttrs)
}

// setNode is called from NewBuilder to populate node attributes.
func (b *Builder) setNode(n *structs.Node) *Builder {
	b.nodeAttrs[nodeIdKey] = n.ID
	b.nodeAttrs[nodeDcKey] = n.Datacenter
	b.nodeAttrs[nodeNameKey] = n.Name
	b.nodeAttrs[nodeClassKey] = n.NodeClass

	// Set up the attributes.
	for k, v := range n.Attributes {
		b.nodeAttrs[fmt.Sprintf("%s%s", nodeAttributePrefix, k)] = v
	}

	// Set up the meta.
	for k, v := range n.Meta {
		b.nodeAttrs[fmt.Sprintf("%s%s", nodeMetaPrefix, k)] = v
	}
	return b
}

func (b *Builder) SetJobName(name string) *Builder {
	b.mu.Lock()
	b.jobName = name
	b.mu.Unlock()
	return b
}

func (b *Builder) SetRegion(r string) *Builder {
	b.mu.Lock()
	b.region = r
	b.mu.Unlock()
	return b
}

func (b *Builder) SetAllocDir(dir string) *Builder {
	b.mu.Lock()
	b.allocDir = dir
	b.mu.Unlock()
	return b
}

func (b *Builder) SetTaskLocalDir(dir string) *Builder {
	b.mu.Lock()
	b.localDir = dir
	b.mu.Unlock()
	return b
}

func (b *Builder) SetSecretsDir(dir string) *Builder {
	b.mu.Lock()
	b.secretsDir = dir
	b.mu.Unlock()
	return b
}

func (b *Builder) SetResources(r *structs.Resources) *Builder {
	if r == nil {
		return b
	}

	// Build up env map for network addresses
	newNetworks := make(map[string]string, len(r.Networks)*4)
	for _, network := range r.Networks {
		for label, intVal := range network.MapLabelToValues(nil) {
			value := strconv.Itoa(intVal)
			newNetworks[fmt.Sprintf("%s%s", IpPrefix, label)] = network.IP
			newNetworks[fmt.Sprintf("%s%s", HostPortPrefix, label)] = value
			if forwardedPort, ok := b.portMap[label]; ok {
				value = forwardedPort
			}
			newNetworks[fmt.Sprintf("%s%s", PortPrefix, label)] = value
			IPPort := net.JoinHostPort(network.IP, value)
			newNetworks[fmt.Sprintf("%s%s", AddrPrefix, label)] = IPPort

		}
	}

	b.mu.Lock()
	b.memLimit = r.MemoryMB
	b.cpuLimit = r.CPU
	b.networks = newNetworks
	b.mu.Unlock()
	return b
}

func (b *Builder) SetPortMap(portMap map[string]int) *Builder {
	newPortMap := make(map[string]string, len(portMap))
	for k, v := range portMap {
		newPortMap[k] = strconv.Itoa(v)
	}
	b.mu.Lock()
	b.portMap = newPortMap
	b.mu.Unlock()
	return b
}

// Takes a map of meta values to be passed to the task. The keys are capatilized
// when the environent variable is set.
func (b *Builder) SetTaskMeta(m map[string]string) *Builder {
	newM := make(map[string]string, len(m)*2)

	for k, v := range m {
		newM[fmt.Sprintf("%s%s", MetaPrefix, strings.ToUpper(k))] = v
		newM[fmt.Sprintf("%s%s", MetaPrefix, k)] = v
	}

	b.mu.Lock()
	b.taskMeta = newM
	b.mu.Unlock()
	return b
}

// Merge the given environment variables into existing ones.
func (b *Builder) MergeEnvvars(m map[string]string) *Builder {
	b.mu.Lock()
	for k, v := range m {
		b.envvars[k] = v
	}
	b.mu.Unlock()
	return b
}

// SetHostEnvvars adds the host environment variables to the tasks. The filter
// parameter can be use to filter host environment from entering the tasks.
func (b *Builder) SetHostEnvvars(filter []string) *Builder {
	filterMap := make(map[string]struct{}, len(filter))
	for _, f := range filter {
		filterMap[f] = struct{}{}
	}

	fullHostEnv := os.Environ()
	filteredHostEnv := make(map[string]string, len(fullHostEnv))
	for _, e := range fullHostEnv {
		parts := strings.SplitN(e, "=", 2)
		key, value := parts[0], parts[1]

		// Skip filtered environment variables
		if _, filtered := filterMap[key]; filtered {
			continue
		}

		filteredHostEnv[key] = value
	}

	b.mu.Lock()
	b.hostEnv = filteredHostEnv
	b.mu.Unlock()
	return b
}

// SetAlloc related environment variables.
func (b *Builder) SetAlloc(alloc *structs.Allocation) *Builder {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.allocId = alloc.ID
	b.allocName = alloc.Name
	b.allocIndex = alloc.Index()

	// Add ports from other tasks
	for taskName, resources := range alloc.TaskResources {
		if taskName == b.taskName {
			continue
		}
		for _, nw := range resources.Networks {
			for _, p := range nw.ReservedPorts {
				addPort(b.otherPorts, b.taskName, nw.IP, p.Label, p.Value)
			}
			for _, p := range nw.DynamicPorts {
				addPort(b.otherPorts, b.taskName, nw.IP, p.Label, p.Value)
			}
		}
	}
	return b
}

func (b *Builder) SetTemplateEnv(m map[string]string) *Builder {
	b.mu.Lock()
	b.templateEnv = m
	b.mu.Unlock()
	return b
}

func (b *Builder) SetVaultToken(token string, inject bool) *Builder {
	b.mu.Lock()
	b.vaultToken = token
	b.injectVaultToken = inject
	b.mu.Unlock()
	return b
}

// addPort keys and values for other tasks to an env var map
func addPort(m map[string]string, taskName, ip, portLabel string, port int) {
	key := fmt.Sprintf("%s%s_%s", AddrPrefix, taskName, portLabel)
	m[key] = fmt.Sprintf("%s:%d", ip, port)
	key = fmt.Sprintf("%s%s_%s", IpPrefix, taskName, portLabel)
	m[key] = ip
	key = fmt.Sprintf("%s%s_%s", PortPrefix, taskName, portLabel)
	m[key] = strconv.Itoa(port)
}
