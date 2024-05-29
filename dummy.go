package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

// DummyDeviceManager manages our dummy devices
type DummyDeviceManager struct {
	devices map[string]*pluginapi.Device
	socket  string
	server  *grpc.Server
	health  chan *pluginapi.Device
}

// Init function for our dummy devices
func (ddm *DummyDeviceManager) Init() error {
	log.Println("Initializing dummy device plugin...")
	return nil
}

// discoverDummyResources populates device list
// TODO: We currently only do this once at init, need to change it to do monitoring
//
//	and health state update
func (ddm *DummyDeviceManager) discoverDummyResources() error {
	log.Println("Discovering dummy devices")
	raw, err := os.ReadFile("./dummyResources.json")
	if err != nil {
		fmt.Println(err.Error())
		return err
	}
	var devs []map[string]string
	err = json.Unmarshal(raw, &devs)
	if err != nil {
		fmt.Println(err)
		return err
	}
	ddm.devices = make(map[string]*pluginapi.Device)
	for _, dev := range devs {
		newdev := pluginapi.Device{ID: dev["name"], Health: pluginapi.Healthy}
		ddm.devices[dev["name"]] = &newdev
	}

	log.Printf("Devices found: %v", ddm.devices)
	return nil
}

// Start starts the gRPC server of the device plugin
func (ddm *DummyDeviceManager) Start() error {
	err := ddm.cleanup()
	if err != nil {
		return err
	}

	sock, err := net.Listen("unix", ddm.socket)
	if err != nil {
		return err
	}

	ddm.server = grpc.NewServer([]grpc.ServerOption{}...)
	pluginapi.RegisterDevicePluginServer(ddm.server, ddm)

	go ddm.server.Serve(sock)

	// Wait for server to start by launching a blocking connection
	conn, err := grpc.Dial(ddm.socket, grpc.WithInsecure(), grpc.WithBlock(),
		grpc.WithTimeout(5*time.Second),
		grpc.WithDialer(func(addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout("unix", addr, timeout)
		}),
	)

	if err != nil {
		return err
	}

	conn.Close()

	go ddm.healthcheck()

	return nil
}

// Stop stops the gRPC server
func (ddm *DummyDeviceManager) Stop() error {
	if ddm.server == nil {
		return nil
	}

	ddm.server.Stop()
	ddm.server = nil

	return ddm.cleanup()
}

// healthcheck monitors and updates device status
// TODO: Currently does nothing, need to implement
func (ddm *DummyDeviceManager) healthcheck() error {
	for {
		log.Println(ddm.devices)
		time.Sleep(60 * time.Second)
	}
}

func (ddm *DummyDeviceManager) cleanup() error {
	if err := os.Remove(ddm.socket); err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

// Register with kubelet
func Register() error {
	conn, err := grpc.Dial(pluginapi.KubeletSocket, grpc.WithInsecure(),
		grpc.WithDialer(func(addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout("unix", addr, timeout)
		}))
	if err != nil {
		return fmt.Errorf("device-plugin: cannot connect to kubelet service: %v", err)
	}
	defer conn.Close()
	client := pluginapi.NewRegistrationClient(conn)
	reqt := &pluginapi.RegisterRequest{
		Version: pluginapi.Version,
		// Name of the unix socket the device plugin is listening on
		// PATH = path.Join(DevicePluginPath, endpoint)
		Endpoint: "dummy.sock",
		// Schedulable resource name.
		ResourceName: "dummy-device-plugin.burgerdev.de/dev-zero",
	}

	_, err = client.Register(context.Background(), reqt)
	if err != nil {
		return fmt.Errorf("device-plugin: cannot register to kubelet service: %v", err)
	}
	return nil
}

// ListAndWatch lists devices and update that list according to the health status
func (ddm *DummyDeviceManager) ListAndWatch(emtpy *pluginapi.Empty, stream pluginapi.DevicePlugin_ListAndWatchServer) error {
	log.Println("device-plugin: ListAndWatch start")
	resp := new(pluginapi.ListAndWatchResponse)
	for _, dev := range ddm.devices {
		log.Println("dev ", dev)
		resp.Devices = append(resp.Devices, dev)
	}
	log.Println("resp.Devices ", resp.Devices)
	if err := stream.Send(resp); err != nil {
		log.Printf("Failed to send response to kubelet: %v", err)
	}

	for d := range ddm.health {
		d.Health = pluginapi.Unhealthy
		resp := new(pluginapi.ListAndWatchResponse)
		for _, dev := range ddm.devices {
			log.Println("dev ", dev)
			resp.Devices = append(resp.Devices, dev)
		}
		log.Println("resp.Devices ", resp.Devices)
		if err := stream.Send(resp); err != nil {
			log.Printf("Failed to send response to kubelet: %v", err)
		}
	}
	return nil
}

// Allocate devices
func (ddm *DummyDeviceManager) Allocate(ctx context.Context, reqs *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	log.Println("Allocate")
	responses := pluginapi.AllocateResponse{}
	for _, req := range reqs.ContainerRequests {
		for _, id := range req.DevicesIDs {
			if _, ok := ddm.devices[id]; !ok {
				log.Printf("Can't allocate interface %s", id)
				return nil, fmt.Errorf("invalid allocation request: unknown device: %s", id)
			}
		}
		log.Println("Allocated interfaces ", req.DevicesIDs)
		response := pluginapi.ContainerAllocateResponse{}
		for _, id := range req.DevicesIDs {
			response.Devices = append(response.Devices, &pluginapi.DeviceSpec{
				HostPath:      "/dev/zero",
				ContainerPath: "/dev/" + id,
				Permissions:   "r",
			})
		}
		responses.ContainerResponses = append(responses.ContainerResponses, &response)
	}
	return &responses, nil
}

// GetDevicePluginOptions returns options to be communicated with Device Manager
func (ddm *DummyDeviceManager) GetDevicePluginOptions(context.Context, *pluginapi.Empty) (*pluginapi.DevicePluginOptions, error) {
	return &pluginapi.DevicePluginOptions{}, nil
}

// PreStartContainer is called, if indicated by Device Plugin during registeration phase,
// before each container start. Device plugin can run device specific operations
// such as reseting the device before making devices available to the container
func (ddm *DummyDeviceManager) PreStartContainer(context.Context, *pluginapi.PreStartContainerRequest) (*pluginapi.PreStartContainerResponse, error) {
	return &pluginapi.PreStartContainerResponse{}, nil
}

func (ddm *DummyDeviceManager) GetPreferredAllocation(ctx context.Context, req *pluginapi.PreferredAllocationRequest) (*pluginapi.PreferredAllocationResponse, error) {
	resp := &pluginapi.PreferredAllocationResponse{}
	for _, creq := range req.ContainerRequests {
		cresp := &pluginapi.ContainerPreferredAllocationResponse{}
		cresp.DeviceIDs = creq.MustIncludeDeviceIDs
		devices := creq.GetAvailableDeviceIDs()
		for i := range int(creq.AllocationSize) - len(cresp.DeviceIDs) {
			if i >= len(devices) {
				break
			}
			cresp.DeviceIDs = append(cresp.DeviceIDs, devices[i])
		}
		resp.ContainerResponses = append(resp.ContainerResponses, cresp)
	}
	return resp, nil
}

func main() {
	flag.Parse()

	// Create new dummy device manager
	ddm := &DummyDeviceManager{
		devices: make(map[string]*pluginapi.Device),
		socket:  pluginapi.DevicePluginPath + "dummy.sock",
		health:  make(chan *pluginapi.Device),
	}

	// Populate device list
	err := ddm.discoverDummyResources()
	if err != nil {
		log.Fatal(err)
	}

	// Respond to syscalls for termination
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	// Start grpc server
	err = ddm.Start()
	if err != nil {
		log.Fatalf("Could not start device plugin: %v", err)
	}
	log.Printf("Starting to serve on %s", ddm.socket)

	// Registers with Kubelet.
	err = Register()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("device-plugin registered\n")

	s := <-sigs
	log.Printf("Received signal \"%v\", shutting down.", s)
	ddm.Stop()
}
