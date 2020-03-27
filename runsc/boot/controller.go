// Copyright 2018 The gVisor Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package boot

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"strconv"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"gvisor.dev/gvisor/pkg/control/server"
	"gvisor.dev/gvisor/pkg/log"
	"gvisor.dev/gvisor/pkg/sentry/control"
	"gvisor.dev/gvisor/pkg/sentry/fs"
	"gvisor.dev/gvisor/pkg/sentry/kernel"
	"gvisor.dev/gvisor/pkg/sentry/socket/netstack"
	"gvisor.dev/gvisor/pkg/sentry/state"
	"gvisor.dev/gvisor/pkg/sentry/time"
	"gvisor.dev/gvisor/pkg/sentry/watchdog"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/urpc"
	"gvisor.dev/gvisor/runsc/boot/pprof"
	"gvisor.dev/gvisor/runsc/specutils"
)

const (
	// ContainerCheckpoint checkpoints a container.
	ContainerCheckpoint = "containerManager.Checkpoint"

	// ContainerCreate creates a container.
	ContainerCreate = "containerManager.Create"

	// ContainerDestroy is used to stop a non-root container and free all
	// associated resources in the sandbox.
	ContainerDestroy = "containerManager.Destroy"

	// ContainerEvent is the URPC endpoint for getting stats about the
	// container used by "runsc events".
	ContainerEvent = "containerManager.Event"

	// ContainerExecuteAsync is the URPC endpoint for executing a command in a
	// container.
	ContainerExecuteAsync = "containerManager.ExecuteAsync"

	// ContainerPause pauses the container.
	ContainerPause = "containerManager.Pause"

	// ContainerProcesses is the URPC endpoint for getting the list of
	// processes running in a container.
	ContainerProcesses = "containerManager.Processes"

	// RootRestore restores the root container from a statefile.
	RootRestore = "containerManager.RootRestore"

	// ContainerRestore restores a container from a statefile.
	ContainerRestore = "containerManager.ContainerRestore"

	// ContainerResume unpauses the paused container.
	ContainerResume = "containerManager.Resume"

	// ContainerSignal is used to send a signal to a container.
	ContainerSignal = "containerManager.Signal"

	// ContainerSignalProcess is used to send a signal to a particular
	// process in a container.
	ContainerSignalProcess = "containerManager.SignalProcess"

	// ContainerStart is the URPC endpoint for running a non-root container
	// within a sandbox.
	ContainerStart = "containerManager.Start"

	// ContainerWait is used to wait on the init process of the container
	// and return its ExitStatus.
	ContainerWait = "containerManager.Wait"

	// ContainerWaitPID is used to wait on a process with a certain PID in
	// the sandbox and return its ExitStatus.
	ContainerWaitPID = "containerManager.WaitPID"

	// NetworkCreateLinksAndRoutes is the URPC endpoint for creating links
	// and routes in a network stack.
	NetworkCreateLinksAndRoutes = "Network.CreateLinksAndRoutes"

	// RootContainerStart is the URPC endpoint for starting a new sandbox
	// with root container.
	RootContainerStart = "containerManager.StartRoot"

	// SandboxStacks collects sandbox stacks for debugging.
	SandboxStacks = "debug.Stacks"
)

// Profiling related commands (see pprof.go for more details).
const (
	StartCPUProfile  = "Profile.StartCPUProfile"
	StopCPUProfile   = "Profile.StopCPUProfile"
	HeapProfile      = "Profile.HeapProfile"
	GoroutineProfile = "Profile.GoroutineProfile"
	BlockProfile     = "Profile.BlockProfile"
	MutexProfile     = "Profile.MutexProfile"
	StartTrace       = "Profile.StartTrace"
	StopTrace        = "Profile.StopTrace"
)

// Logging related commands (see logging.go for more details).
const (
	ChangeLogging = "Logging.Change"
)

// ControlSocketAddr generates an abstract unix socket name for the given ID.
func ControlSocketAddr(id string) string {
	return fmt.Sprintf("\x00runsc-sandbox.%s", id)
}

// controller holds the control server, and is used for communication into the
// sandbox.
type controller struct {
	// srv is the control server.
	srv *server.Server

	// manager holds the containerManager methods.
	manager *containerManager
}

// newController creates a new controller. The caller must call
// controller.srv.StartServing() to start the controller.
func newController(fd int, l *Loader) (*controller, error) {
	srv, err := server.CreateFromFD(fd)
	if err != nil {
		return nil, err
	}

	manager := &containerManager{
		startChan:         make(chan struct{}),
		startResultChan:   make(chan error),
		restoreStartChan:  make(chan struct{}),
		restoreResultChan: make(chan error),
		l:                 l,
	}
	srv.Register(manager)

	if eps, ok := l.k.RootNetworkNamespace().Stack().(*netstack.Stack); ok {
		net := &Network{
			Stack: eps.Stack,
		}
		srv.Register(net)
	}

	srv.Register(&debug{})
	srv.Register(&control.Logging{})
	if l.conf.ProfileEnable {
		srv.Register(&control.Profile{
			Kernel: l.k,
		})
	}

	return &controller{
		srv:     srv,
		manager: manager,
	}, nil
}

// containerManager manages sandbox containers.
type containerManager struct {
	// startChan is used to signal when the root container process should
	// be started.
	startChan chan struct{}

	// startResultChan is used to signal when the root container  has
	// started. Any errors encountered during startup will be sent to the
	// channel. A nil value indicates success.
	startResultChan chan error

	// l is the loader that creates containers and sandboxes.
	l *Loader

	// cindex is the current index of the containers inside a sandbox
	cindex int

	// total container number retrieved from checkpoint image
	totalContainerNum int

	// restoreStartChan is used to block Restore till all child
	// containers have called restore cmd
	restoreStartChan chan struct{}

	// restoreResultChan is used to tell child container the
	// result of restore
	restoreResultChan chan error
}

// StartRoot will start the root container process.
func (cm *containerManager) StartRoot(cid *string, _ *struct{}) error {
	log.Debugf("containerManager.StartRoot %q", *cid)
	// Tell the root container to start and wait for the result.
	cm.startChan <- struct{}{}
	if err := <-cm.startResultChan; err != nil {
		return fmt.Errorf("starting sandbox: %v", err)
	}
	return nil
}

// Processes retrieves information about processes running in the sandbox.
func (cm *containerManager) Processes(cid *string, out *[]*control.Process) error {
	log.Debugf("containerManager.Processes: %q", *cid)
	return control.Processes(cm.l.k, cm.l.containers[*cid], out)
}

// Create creates a container within a sandbox.
func (cm *containerManager) Create(cid *string, _ *struct{}) error {
	log.Debugf("containerManager.Create: %q", *cid)
	return cm.l.createContainer(*cid)
}

// StartArgs contains arguments to the Start method.
type StartArgs struct {
	// Spec is the spec of the container to start.
	Spec *specs.Spec

	// Config is the runsc-specific configuration for the sandbox.
	Conf *Config

	// CID is the ID of the container to start.
	CID string

	// FilePayload contains, in order:
	//   * stdin, stdout, and stderr.
	//   * the file descriptor over which the sandbox will
	//     request files from its root filesystem.
	urpc.FilePayload
}

// Start runs a created container within a sandbox.
func (cm *containerManager) Start(args *StartArgs, _ *struct{}) error {
	log.Debugf("containerManager.Start: %+v", args)

	// Validate arguments.
	if args == nil {
		return errors.New("start missing arguments")
	}
	if args.Spec == nil {
		return errors.New("start arguments missing spec")
	}
	if args.Conf == nil {
		return errors.New("start arguments missing config")
	}
	if args.CID == "" {
		return errors.New("start argument missing container ID")
	}
	if len(args.FilePayload.Files) < 4 {
		return fmt.Errorf("start arguments must contain stdin, stderr, and stdout followed by at least one file for the container root gofer")
	}

	// All validation passed, logs the spec for debugging.
	specutils.LogSpec(args.Spec)

	err := cm.l.startContainer(args.Spec, args.Conf, args.CID, args.FilePayload.Files)
	if err != nil {
		log.Debugf("containerManager.Start failed %q: %+v: %v", args.CID, args, err)
		return err
	}
	log.Debugf("Container %q started", args.CID)

	return nil
}

// Destroy stops a container if it is still running and cleans up its
// filesystem.
func (cm *containerManager) Destroy(cid *string, _ *struct{}) error {
	log.Debugf("containerManager.destroy %q", *cid)
	return cm.l.destroyContainer(*cid)
}

// ExecuteAsync starts running a command on a created or running sandbox. It
// returns the PID of the new process.
func (cm *containerManager) ExecuteAsync(args *control.ExecArgs, pid *int32) error {
	log.Debugf("containerManager.ExecuteAsync: %+v", args)
	tgid, err := cm.l.executeAsync(args)
	if err != nil {
		log.Debugf("containerManager.ExecuteAsync failed: %+v: %v", args, err)
		return err
	}
	*pid = int32(tgid)
	return nil
}

// Checkpoint pauses a sandbox and saves its state.
func (cm *containerManager) Checkpoint(o *control.SaveOpts, _ *struct{}) error {
	log.Debugf("containerManager.Checkpoint")
	state := control.State{
		Kernel:   cm.l.k,
		Watchdog: cm.l.watchdog,
	}

	if o.Metadata == nil {
		o.Metadata = make(map[string]string)
	}
	o.Metadata["container_num"] = strconv.Itoa(cm.cindex + 1)
	return state.Save(o, nil)
}

// Pause suspends a container.
func (cm *containerManager) Pause(_, _ *struct{}) error {
	log.Debugf("containerManager.Pause")
	cm.l.k.Pause()
	return nil
}

// RestoreOpts contains options related to restoring a container's file system.
type RestoreOpts struct {
	// FilePayload contains the state file to be restored, followed by the
	// platform device file if necessary.
	urpc.FilePayload

	// SandboxID contains the ID of the sandbox.
	SandboxID string

	// below fields are only useful for child containers

	// ID of the to be restored child container
	CID string

	// spec and conf of the to be restored child container.
	// root container's spec and conf are stored in loader.
	Spec *specs.Spec
	Conf *Config
}

// RootRestore loads a container from a statefile.
// The container's current kernel is destroyed, a restore environment is
// created, and the kernel is recreated with the restore state file. The
// container then sends the signal to start.
func (cm *containerManager) RootRestore(o *RestoreOpts, _ *struct{}) error {
	log.Debugf("containerManager.RootRestore")

	var specFile, deviceFile *os.File
	switch numFiles := len(o.FilePayload.Files); numFiles {
	case 2:
		// The device file is donated to the platform.
		// Can't take ownership away from os.File. dup them to get a new FD.
		fd, err := syscall.Dup(int(o.FilePayload.Files[1].Fd()))
		if err != nil {
			return fmt.Errorf("failed to dup file: %v", err)
		}
		deviceFile = os.NewFile(uintptr(fd), "platform device")
		fallthrough
	case 1:
		specFile = o.FilePayload.Files[0]
	case 0:
		return fmt.Errorf("at least one file must be passed to Restore")
	default:
		return fmt.Errorf("at most two files may be passed to Restore")
	}

	// Pause the kernel while we build a new one.
	cm.l.k.Pause()

	p, err := createPlatform(cm.l.conf, deviceFile)
	if err != nil {
		return fmt.Errorf("creating platform: %v", err)
	}
	k := &kernel.Kernel{
		Platform: p,
	}
	mf, err := createMemoryFile()
	if err != nil {
		return fmt.Errorf("creating memory file: %v", err)
	}
	k.SetMemoryFile(mf)
	networkStack := cm.l.k.RootNetworkNamespace().Stack()
	cm.l.k = k

	// Set up the restore environment.
	mntr := newContainerMounter(cm.l.spec, cm.l.goferFDs, cm.l.k, cm.l.mountHints)
	renv, err := mntr.createRestoreEnvironment(cm.l.conf)
	if err != nil {
		return fmt.Errorf("creating RestoreEnvironment: %v", err)
	}
	fs.SetRestoreEnvironment(*renv)

	// get container number saved in the image
	loadOpts := state.LoadOpts{Source: specFile}
	m, err := loadOpts.GetMetadata()
	if err != nil {
		log.Infof("get statefile's metadata error %v\n", err)
		return err
	}
	cm.totalContainerNum, err = strconv.Atoi(m["container_num"])
	if err != nil {
		return fmt.Errorf("strconv Atoi %v failed. %v", m["container_num"], err)
	}

	// wait till all child containers call restore
	for i := 0; i < cm.totalContainerNum - 1; i++ {
		<-cm.restoreStartChan
	}

	// Prepare to load from the state file.
	if eps, ok := networkStack.(*netstack.Stack); ok {
		stack.StackFromEnv = eps.Stack // FIXME(b/36201077)
	}
	info, err := specFile.Stat()
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return fmt.Errorf("file cannot be empty")
	}

	if cm.l.conf.ProfileEnable {
		// pprof.Initialize opens /proc/self/maps, so has to be called before
		// installing seccomp filters.
		pprof.Initialize()
	}

	// Seccomp filters have to be applied before parsing the state file.
	if err := cm.l.installSeccompFilters(); err != nil {
		return err
	}

	// loadOpts.Load() expects specFile's offset at 0 and
	// we have moved its offet in loadOpts.GetMetadata()
	specFile.Seek(0, 0)

	// Load the state.
	if err := loadOpts.Load(k, networkStack, time.NewCalibratedClocks()); err != nil {
		for i := 0; i < cm.totalContainerNum - 1; i++ {
			cm.restoreResultChan <- err
		}
		return err
	}

	// Since we have a new kernel we also must make a new watchdog.
	dogOpts := watchdog.DefaultOpts
	dogOpts.TaskTimeoutAction = cm.l.conf.WatchdogAction
	dog := watchdog.New(k, dogOpts)

	// Change the loader fields to reflect the changes made when restoring.
	cm.l.k = k
	cm.l.watchdog = dog
	cm.l.rootProcArgs = kernel.CreateProcessArgs{}
	cm.l.restore = true

	// Reinitialize the sandbox ID and processes map.
	cm.l.sandboxID = o.SandboxID
	cm.l.mu.Lock()
	for cid, index := range cm.l.containers {
		eid := execID{cid: cid}
		cm.l.processes[eid] = &execProcess{
			tg: cm.l.k.ContainerInit(index),
		}
	}
	cm.l.mu.Unlock()

	// Update the restored kernel's rootUTSNamespace according to the restore spec
	// Loader.spec contains hostname information for the restore root container
	cm.l.k.UpdateRootUTSNamespace(cm.l.spec.Hostname, cm.l.spec.Hostname)

	// Tell the root container to start and wait for the result.
	cm.startChan <- struct{}{}
	if err := <-cm.startResultChan; err != nil {
		for i := 0; i < cm.totalContainerNum - 1; i++ {
			cm.restoreResultChan <- err
		}
		return fmt.Errorf("starting sandbox: %v", err)
	}

	// tell child container to continue
	for i := 0; i < cm.totalContainerNum - 1; i++ {
		cm.restoreResultChan <- nil
	}
	return nil
}

func (cm *containerManager) ContainerRestore(o *RestoreOpts, _ *struct{}) error {
	log.Debugf("containerManager.ContainerRestore")

	// make sure stdioFDs are the same as when this container first started
	myindex := cm.l.containers[o.CID]
	var stdioFDs []int
	newfd := (myindex + 1) * startingStdioFD
	for _, f := range o.FilePayload.Files[:3] {
		err := syscall.Dup3(int(f.Fd()), newfd, syscall.O_CLOEXEC)
		if err != nil {
			fmt.Errorf("dup3 of child container stdioFDs")
		}
		stdioFDs = append(stdioFDs, newfd)
		newfd++
	}

	// Can't take ownership away from os.File. dup them to get a new FDs.
	var goferFDs []int
	for _, f := range o.FilePayload.Files[3:] {
		fd, err := syscall.Dup(int(f.Fd()))
		if err != nil {
			return fmt.Errorf("failed to dup file: %v", err)
		}
		goferFDs = append(goferFDs, fd)
	}

	// Set up the restore environment for child container.
	mntr := newContainerMounter(o.Spec, goferFDs, cm.l.k, cm.l.mountHints)
	renv, err := mntr.createRestoreEnvironment(o.Conf)
	if err != nil {
		return fmt.Errorf("creating ContainerRestoreEnvironment: %v", err)
	}
	fs.SetRestoreEnvironment(*renv)

	// tell RootRestore to start
	cm.restoreStartChan <- struct{}{}

	// wait till restore done
	err = <-cm.restoreResultChan
	if err != nil {
		return err
	}

	// the dup()ed stdioFDs are no longer needed
	for fd := range stdioFDs {
		err = syscall.Close(fd)
		if err != nil {
			fmt.Errorf("close dup()ed child container stdioFDs: %v", err)
		}
	}

	return nil
}

// Resume unpauses a container.
func (cm *containerManager) Resume(_, _ *struct{}) error {
	log.Debugf("containerManager.Resume")
	cm.l.k.Unpause()
	return nil
}

// Wait waits for the init process in the given container.
func (cm *containerManager) Wait(cid *string, waitStatus *uint32) error {
	log.Debugf("containerManager.Wait")
	err := cm.l.waitContainer(*cid, waitStatus)
	log.Debugf("containerManager.Wait returned, waitStatus: %v: %v", waitStatus, err)
	return err
}

// WaitPIDArgs are arguments to the WaitPID method.
type WaitPIDArgs struct {
	// PID is the PID in the container's PID namespace.
	PID int32

	// CID is the container ID.
	CID string
}

// WaitPID waits for the process with PID 'pid' in the sandbox.
func (cm *containerManager) WaitPID(args *WaitPIDArgs, waitStatus *uint32) error {
	log.Debugf("containerManager.Wait")
	return cm.l.waitPID(kernel.ThreadID(args.PID), args.CID, waitStatus)
}

// SignalDeliveryMode enumerates different signal delivery modes.
type SignalDeliveryMode int

const (
	// DeliverToProcess delivers the signal to the container process with
	// the specified PID. If PID is 0, then the container init process is
	// signaled.
	DeliverToProcess SignalDeliveryMode = iota

	// DeliverToAllProcesses delivers the signal to all processes in the
	// container. PID must be 0.
	DeliverToAllProcesses

	// DeliverToForegroundProcessGroup delivers the signal to the
	// foreground process group in the same TTY session as the specified
	// process. If PID is 0, then the signal is delivered to the foreground
	// process group for the TTY for the init process.
	DeliverToForegroundProcessGroup
)

func (s SignalDeliveryMode) String() string {
	switch s {
	case DeliverToProcess:
		return "Process"
	case DeliverToAllProcesses:
		return "All"
	case DeliverToForegroundProcessGroup:
		return "Foreground Process Group"
	}
	return fmt.Sprintf("unknown signal delivery mode: %d", s)
}

// SignalArgs are arguments to the Signal method.
type SignalArgs struct {
	// CID is the container ID.
	CID string

	// Signo is the signal to send to the process.
	Signo int32

	// PID is the process ID in the given container that will be signaled.
	// If 0, the root container will be signalled.
	PID int32

	// Mode is the signal delivery mode.
	Mode SignalDeliveryMode
}

// Signal sends a signal to one or more processes in a container. If args.PID
// is 0, then the container init process is used. Depending on the
// args.SignalDeliveryMode option, the signal may be sent directly to the
// indicated process, to all processes in the container, or to the foreground
// process group.
func (cm *containerManager) Signal(args *SignalArgs, _ *struct{}) error {
	log.Debugf("containerManager.Signal %+v", args)
	return cm.l.signal(args.CID, args.PID, args.Signo, args.Mode)
}
