//go:build windows

package platform

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"golang.org/x/sys/windows"
)

const (
	driveRemote               = 4
	resourceTypeDisk          = 1
	createNewProcessGroup     = 0x00000200
	ctrlBreakEvent            = 1
	processQueryInformation   = 0x0400
	processVMRead             = 0x0010
	processQueryLimitedInfo   = 0x1000
	th32csSnapProcess         = 0x00000002
	maxPath                   = 260
	windowsToUnixEpoch100Nano = 116444736000000000
	afINET                    = 2
	afINET6                   = 23
	tcpTableOwnerPIDListener  = 3
	errorInsufficientBuffer   = 122
)

var (
	kernel32                        = syscall.NewLazyDLL("kernel32.dll")
	mpr                             = syscall.NewLazyDLL("mpr.dll")
	psapi                           = syscall.NewLazyDLL("psapi.dll")
	iphlpapi                        = syscall.NewLazyDLL("iphlpapi.dll")
	procGetLogicalDriveStringsW     = kernel32.NewProc("GetLogicalDriveStringsW")
	procGetDriveTypeW               = kernel32.NewProc("GetDriveTypeW")
	procGetVolumeNameForMountPointW = kernel32.NewProc("GetVolumeNameForVolumeMountPointW")
	procGetVolumeInformationW       = kernel32.NewProc("GetVolumeInformationW")
	procGenerateConsoleCtrlEvent    = kernel32.NewProc("GenerateConsoleCtrlEvent")
	procOpenProcess                 = kernel32.NewProc("OpenProcess")
	procCloseHandle                 = kernel32.NewProc("CloseHandle")
	procQueryFullProcessImageNameW  = kernel32.NewProc("QueryFullProcessImageNameW")
	procGetProcessTimes             = kernel32.NewProc("GetProcessTimes")
	procCreateToolhelp32Snapshot    = kernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32FirstW             = kernel32.NewProc("Process32FirstW")
	procProcess32NextW              = kernel32.NewProc("Process32NextW")
	procWNetGetConnectionW          = mpr.NewProc("WNetGetConnectionW")
	procWNetAddConnection2W         = mpr.NewProc("WNetAddConnection2W")
	procWNetGetResourceInformationW = mpr.NewProc("WNetGetResourceInformationW")
	procGetProcessMemoryInfo        = psapi.NewProc("GetProcessMemoryInfo")
	procGetExtendedTCPTable         = iphlpapi.NewProc("GetExtendedTcpTable")
)

type windowsBackend struct {
	mu      sync.Mutex
	job     windows.Handle
	samples map[int]processCPUSample
}

type processCPUSample struct {
	at    time.Time
	ticks uint64
}

type netResource struct {
	Scope       uint32
	Type        uint32
	DisplayType uint32
	Usage       uint32
	LocalName   *uint16
	RemoteName  *uint16
	Comment     *uint16
	Provider    *uint16
}

type processMemoryCounters struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

type processEntry32 struct {
	Size            uint32
	CntUsage        uint32
	ProcessID       uint32
	DefaultHeapID   uintptr
	ModuleID        uint32
	Threads         uint32
	ParentProcessID uint32
	PriClassBase    int32
	Flags           uint32
	ExeFile         [maxPath]uint16
}

type windowsProcess struct {
	ProcessID      uint32 `json:"ProcessId"`
	ExecutablePath string `json:"ExecutablePath"`
	CommandLine    string `json:"CommandLine"`
}

func newBackend() Backend { return &windowsBackend{samples: make(map[int]processCPUSample)} }

func (w *windowsBackend) Mounts(ctx context.Context) ([]MountInfo, error) {
	drives, err := logicalDrives()
	if err != nil {
		return nil, err
	}
	mounts := make([]MountInfo, 0, len(drives))
	for _, target := range drives {
		root, err := syscall.UTF16PtrFromString(target)
		if err != nil {
			continue
		}
		driveType, _, _ := procGetDriveTypeW.Call(uintptr(unsafe.Pointer(root)))
		if driveType == driveRemote {
			source, err := networkSource(target)
			if err == nil {
				filesystem := "smb"
				provider, _ := networkProvider(source)
				if strings.Contains(strings.ToUpper(provider), "NFS") || strings.Contains(source, ":/") {
					filesystem = "nfs"
					source = normalizeWindowsNFSSource(source)
				}
				mounts = append(mounts, MountInfo{Source: source, Target: target, FSType: filesystem})
			}
			continue
		}
		volume, err := volumeNameForMountPoint(target)
		if err != nil {
			continue
		}
		_, filesystem, err := volumeInformation(target)
		if err != nil {
			continue
		}
		mounts = append(mounts, MountInfo{Source: volume, Target: target, FSType: filesystem})
	}
	if _, err := windowsSystemMountExecutable(); err == nil {
		nfsMounts, err := windowsNFSMounts(ctx)
		if err != nil {
			return nil, err
		}
		mounts = mergeWindowsNFSMounts(mounts, nfsMounts)
	}
	return mounts, nil
}

func mergeWindowsNFSMounts(mounts, nfsMounts []MountInfo) []MountInfo {
	for _, nfsMount := range nfsMounts {
		replaced := false
		for index := range mounts {
			if strings.EqualFold(mounts[index].Target, nfsMount.Target) {
				mounts[index] = nfsMount
				replaced = true
				break
			}
		}
		if !replaced {
			mounts = append(mounts, nfsMount)
		}
	}
	return mounts
}

func (w *windowsBackend) ResolvePhysical(_ context.Context, disk config.DiskConfig) (string, error) {
	volume := canonicalVolumeGUID(disk.VolumeGUID)
	label, filesystem, err := volumeInformation(volume)
	if err != nil {
		return "", fmt.Errorf("inspect Windows volume %s: %w", volume, err)
	}
	if disk.VolumeLabel != "" && !strings.EqualFold(label, disk.VolumeLabel) {
		return "", MountIdentityError{Err: fmt.Errorf("volume label mismatch: got %q, want %q", label, disk.VolumeLabel)}
	}
	if disk.Filesystem != "" && !strings.EqualFold(filesystem, disk.Filesystem) {
		return "", MountIdentityError{Err: fmt.Errorf("filesystem mismatch: got %q, want %q", filesystem, disk.Filesystem)}
	}
	return volume, nil
}

func (w *windowsBackend) Mount(ctx context.Context, disk config.DiskConfig) error {
	switch disk.Type {
	case "physical":
		expected, err := w.ResolvePhysical(ctx, disk)
		if err != nil {
			return err
		}
		actual, err := volumeNameForMountPoint(disk.Target)
		if err != nil {
			return fmt.Errorf("physical volume is not mounted at %s: %w", disk.Target, err)
		}
		if !strings.EqualFold(canonicalVolumeGUID(actual), expected) {
			return fmt.Errorf("target %s belongs to %s, want %s", disk.Target, actual, expected)
		}
		return nil
	case "smb":
		if actual, err := networkSource(disk.Target); err == nil {
			if sameSMBSource(actual, disk.Device) {
				return nil
			}
			return fmt.Errorf("SMB target %s is already mapped to %s, want %s", disk.Target, actual, disk.Device)
		}
		return connectNetworkDrive(disk)
	case "nfs":
		return mountWindowsNFS(ctx, disk)
	default:
		return fmt.Errorf("unsupported disk type %q", disk.Type)
	}
}

var windowsNFSMountLine = regexp.MustCompile(`(?m)^\s*([A-Za-z]:)\s+(\S+)`)

func windowsNFSMounts(ctx context.Context) ([]MountInfo, error) {
	mount, err := windowsSystemMountExecutable()
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, mount)
	command.Env = os.Environ()
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("list Windows NFS mounts: %w", err)
	}
	return parseWindowsNFSMounts(string(output)), nil
}

func parseWindowsNFSMounts(output string) []MountInfo {
	matches := windowsNFSMountLine.FindAllStringSubmatch(output, -1)
	mounts := make([]MountInfo, 0, len(matches))
	for _, match := range matches {
		if len(match) != 3 {
			continue
		}
		mounts = append(mounts, MountInfo{
			Source: normalizeWindowsNFSSource(match[2]),
			Target: strings.ToUpper(match[1][:1]) + `:\`,
			FSType: "nfs",
		})
	}
	return mounts
}

func mountWindowsNFS(ctx context.Context, disk config.DiskConfig) error {
	mount, err := windowsSystemMountExecutable()
	if err != nil {
		return errors.New("Windows Client for NFS is not installed; enable ServicesForNFS-ClientOnly and ClientForNFS-Infrastructure")
	}
	target := strings.TrimSuffix(windowsCleanDriveTarget(disk.Target), `\`)
	if len(target) != 2 || target[1] != ':' {
		return fmt.Errorf("Windows NFS target must be a drive root such as Z:\\: %s", disk.Target)
	}
	args := make([]string, 0, 4)
	if strings.TrimSpace(disk.Options) != "" {
		options, err := windowsNFSOptions(disk.Options)
		if err != nil {
			return err
		}
		args = append(args, "-o")
		args = append(args, strings.Join(options, ","))
	}
	args = append(args, windowsNFSCommandSource(disk.Device), target)
	command := exec.CommandContext(ctx, mount, args...)
	command.Env = os.Environ()
	if output, err := command.CombinedOutput(); err != nil {
		message := strings.TrimSpace(string(output))
		if message != "" {
			return fmt.Errorf("mount Windows NFS %s at %s: %s", disk.Device, disk.Target, message)
		}
		return fmt.Errorf("mount Windows NFS %s at %s: %w", disk.Device, disk.Target, err)
	}
	return nil
}

func windowsNFSOptions(value string) ([]string, error) {
	options := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
	for _, option := range options {
		lower := strings.ToLower(option)
		valid := lower == "anon" || lower == "nolock"
		for _, prefix := range []string{"rsize=", "wsize=", "timeout=", "retry=", "mtype=", "lang=", "fileaccess=", "casesensitive=", "sec="} {
			valid = valid || strings.HasPrefix(lower, prefix)
		}
		if !valid {
			return nil, fmt.Errorf("unsupported Windows NFS mount option %q", option)
		}
	}
	return options, nil
}

func windowsSystemMountExecutable() (string, error) {
	root := strings.TrimSpace(os.Getenv("SystemRoot"))
	if root == "" {
		root = `C:\Windows`
	}
	path := filepath.Join(root, "System32", "mount.exe")
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		if err == nil {
			err = os.ErrNotExist
		}
		return "", err
	}
	return path, nil
}

func windowsNFSCommandSource(source string) string {
	source = strings.TrimSpace(source)
	if strings.HasPrefix(source, `\\`) || strings.HasPrefix(source, "//") {
		return `\\` + strings.TrimLeft(strings.ReplaceAll(source, "/", `\`), `\`)
	}
	if separator := strings.Index(source, ":/"); separator > 0 {
		return `\\` + source[:separator] + `\` + strings.ReplaceAll(strings.TrimPrefix(source[separator+1:], "/"), "/", `\`)
	}
	return source
}

func normalizeWindowsNFSSource(source string) string {
	source = strings.TrimSpace(strings.ReplaceAll(source, `\`, "/"))
	source = strings.TrimPrefix(source, "//")
	if strings.Contains(source, ":/") {
		return source
	}
	if separator := strings.IndexByte(source, '/'); separator > 0 {
		return source[:separator] + ":" + source[separator:]
	}
	return source
}

func windowsCleanDriveTarget(target string) string {
	target = filepath.Clean(strings.TrimSpace(target))
	if len(target) >= 2 && target[1] == ':' {
		return strings.ToUpper(target[:1]) + target[1:]
	}
	return target
}

func (*windowsBackend) ExecutableProvenance(string) (bool, error) { return false, nil }

func (*windowsBackend) ConfigureProcess(cmd *exec.Cmd, username, groupname string) error {
	if username != "" || groupname != "" {
		return errors.New("jellyfin.run-as-user and run-as-group are not supported on Windows; configure the Windows service identity instead")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
	return nil
}

func (w *windowsBackend) AttachProcess(pid int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.job == 0 {
		job, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			return fmt.Errorf("create Windows Job Object: %w", err)
		}
		limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
			windows.CloseHandle(job)
			return fmt.Errorf("configure Windows Job Object: %w", err)
		}
		w.job = job
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("open process %d for Job Object: %w", pid, err)
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(w.job, process); err != nil {
		return fmt.Errorf("assign process %d to Windows Job Object: %w", pid, err)
	}
	return nil
}

func (w *windowsBackend) SignalGroup(pid int, force bool) error {
	if force {
		w.mu.Lock()
		job := w.job
		w.mu.Unlock()
		if job != 0 {
			if err := windows.TerminateJobObject(job, 1); err != nil {
				return fmt.Errorf("terminate Windows Job Object: %w", err)
			}
			return nil
		}
		process, err := os.FindProcess(pid)
		if err != nil {
			return err
		}
		return process.Kill()
	}
	r1, _, err := procGenerateConsoleCtrlEvent.Call(ctrlBreakEvent, uintptr(uint32(pid)))
	if r1 == 0 {
		return fmt.Errorf("send CTRL_BREAK_EVENT to process group %d: %w", pid, errnoError(err))
	}
	return nil
}

func (w *windowsBackend) ProcessInfo(_ context.Context, pid int) (ProcessInfo, error) {
	handle, err := openProcess(pid, processQueryInformation|processVMRead|processQueryLimitedInfo)
	if err != nil {
		return ProcessInfo{}, err
	}
	defer procCloseHandle.Call(handle)
	path, err := processImagePath(handle)
	if err != nil {
		return ProcessInfo{}, err
	}
	var memory processMemoryCounters
	memory.CB = uint32(unsafe.Sizeof(memory))
	if r1, _, callErr := procGetProcessMemoryInfo.Call(handle, uintptr(unsafe.Pointer(&memory)), uintptr(memory.CB)); r1 == 0 {
		return ProcessInfo{}, fmt.Errorf("read process memory: %w", errnoError(callErr))
	}
	startedAt, cpuTicks, _ := processTimes(handle)
	cpuPercent := w.cpuPercent(pid, cpuTicks)
	return ProcessInfo{
		PID:             pid,
		PGID:            pid,
		State:           "R",
		Command:         path,
		MemoryBytes:     uint64(memory.WorkingSetSize),
		CPUPercent:      cpuPercent,
		FFmpegProcesses: countWindowsFFmpeg(pid),
		Ports:           windowsListeningPorts(pid),
		StartedAt:       startedAt,
	}, nil
}

func windowsListeningPorts(pid int) []int {
	seen := make(map[int]bool)
	for _, table := range []struct {
		family     uint32
		rowSize    int
		portOffset int
		pidOffset  int
	}{
		{family: afINET, rowSize: 24, portOffset: 8, pidOffset: 20},
		{family: afINET6, rowSize: 56, portOffset: 20, pidOffset: 52},
	} {
		data, err := windowsTCPTable(table.family)
		if err != nil {
			continue
		}
		for _, port := range parseWindowsTCPPorts(data, table.rowSize, table.portOffset, table.pidOffset, uint32(pid)) {
			seen[port] = true
		}
	}
	ports := make([]int, 0, len(seen))
	for port := range seen {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ports
}

func windowsTCPTable(family uint32) ([]byte, error) {
	var size uint32
	r1, _, _ := procGetExtendedTCPTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, uintptr(family), tcpTableOwnerPIDListener, 0)
	if r1 != errorInsufficientBuffer && r1 != 0 {
		return nil, syscall.Errno(r1)
	}
	if size < 4 || size > 64<<20 {
		return nil, fmt.Errorf("GetExtendedTcpTable returned invalid size %d", size)
	}
	buffer := make([]byte, size)
	r1, _, _ = procGetExtendedTCPTable.Call(
		uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)), 0,
		uintptr(family), tcpTableOwnerPIDListener, 0)
	if r1 != 0 {
		return nil, syscall.Errno(r1)
	}
	return buffer[:size], nil
}

func parseWindowsTCPPorts(data []byte, rowSize, portOffset, pidOffset int, pid uint32) []int {
	if len(data) < 4 || rowSize <= 0 || portOffset < 0 || pidOffset < 0 || portOffset+4 > rowSize || pidOffset+4 > rowSize {
		return nil
	}
	count := int(binary.LittleEndian.Uint32(data[:4]))
	if count > (len(data)-4)/rowSize {
		count = (len(data) - 4) / rowSize
	}
	ports := make([]int, 0)
	for index := 0; index < count; index++ {
		row := data[4+index*rowSize : 4+(index+1)*rowSize]
		if binary.LittleEndian.Uint32(row[pidOffset:pidOffset+4]) != pid {
			continue
		}
		raw := uint16(binary.LittleEndian.Uint32(row[portOffset : portOffset+4]))
		port := int(raw>>8 | raw<<8)
		if port > 0 {
			ports = append(ports, port)
		}
	}
	return ports
}

func (w *windowsBackend) cpuPercent(pid int, ticks uint64) float64 {
	now := time.Now()
	w.mu.Lock()
	defer w.mu.Unlock()
	previous, ok := w.samples[pid]
	w.samples[pid] = processCPUSample{at: now, ticks: ticks}
	if !ok || ticks < previous.ticks {
		return 0
	}
	elapsed := now.Sub(previous.at)
	if elapsed <= 0 {
		return 0
	}
	return float64(ticks-previous.ticks) * 100 / float64(elapsed.Nanoseconds()/100)
}

func (w *windowsBackend) FindProcesses(ctx context.Context, executable string, requiredArgs []string) ([]ProcessInfo, error) {
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"Get-CimInstance Win32_Process | Select-Object ProcessId,ExecutablePath,CommandLine | ConvertTo-Json -Compress")
	command.Env = os.Environ()
	b, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("enumerate Windows processes: %w", err)
	}
	var processes []windowsProcess
	if err := json.Unmarshal(b, &processes); err != nil {
		var one windowsProcess
		if oneErr := json.Unmarshal(b, &one); oneErr != nil {
			return nil, fmt.Errorf("decode Windows process list: %w", err)
		}
		processes = []windowsProcess{one}
	}
	var matches []ProcessInfo
	for _, process := range processes {
		if process.ProcessID == 0 || process.ExecutablePath == "" || !sameWindowsPath(process.ExecutablePath, executable) {
			continue
		}
		arguments, err := commandLineArguments(process.CommandLine)
		if err != nil || !containsRequiredArguments(arguments, requiredArgs) {
			continue
		}
		info, err := w.ProcessInfo(ctx, int(process.ProcessID))
		if err != nil {
			continue
		}
		info.Command = process.CommandLine
		info.Arguments = arguments
		matches = append(matches, info)
	}
	return matches, nil
}

func logicalDrives() ([]string, error) {
	required, _, err := procGetLogicalDriveStringsW.Call(0, 0)
	if required == 0 {
		return nil, fmt.Errorf("GetLogicalDriveStringsW size: %w", errnoError(err))
	}
	buffer := make([]uint16, required+1)
	written, _, err := procGetLogicalDriveStringsW.Call(uintptr(len(buffer)), uintptr(unsafe.Pointer(&buffer[0])))
	if written == 0 {
		return nil, fmt.Errorf("GetLogicalDriveStringsW: %w", errnoError(err))
	}
	var drives []string
	for start := 0; start < int(written); {
		end := start
		for end < len(buffer) && buffer[end] != 0 {
			end++
		}
		if end == start {
			break
		}
		drives = append(drives, syscall.UTF16ToString(buffer[start:end]))
		start = end + 1
	}
	return drives, nil
}

func volumeNameForMountPoint(target string) (string, error) {
	target = ensureTrailingBackslash(target)
	input, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return "", err
	}
	buffer := make([]uint16, 64)
	r1, _, callErr := procGetVolumeNameForMountPointW.Call(
		uintptr(unsafe.Pointer(input)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if r1 == 0 {
		return "", errnoError(callErr)
	}
	return syscall.UTF16ToString(buffer), nil
}

func volumeInformation(root string) (string, string, error) {
	rootPtr, err := syscall.UTF16PtrFromString(ensureTrailingBackslash(root))
	if err != nil {
		return "", "", err
	}
	label := make([]uint16, 261)
	filesystem := make([]uint16, 32)
	var serial, maxComponent, flags uint32
	r1, _, callErr := procGetVolumeInformationW.Call(
		uintptr(unsafe.Pointer(rootPtr)),
		uintptr(unsafe.Pointer(&label[0])), uintptr(len(label)),
		uintptr(unsafe.Pointer(&serial)), uintptr(unsafe.Pointer(&maxComponent)), uintptr(unsafe.Pointer(&flags)),
		uintptr(unsafe.Pointer(&filesystem[0])), uintptr(len(filesystem)),
	)
	if r1 == 0 {
		return "", "", errnoError(callErr)
	}
	return syscall.UTF16ToString(label), syscall.UTF16ToString(filesystem), nil
}

func networkSource(target string) (string, error) {
	localName := strings.TrimSuffix(target, `\`)
	local, err := syscall.UTF16PtrFromString(localName)
	if err != nil {
		return "", err
	}
	size := uint32(32768)
	buffer := make([]uint16, size)
	r1, _, _ := procWNetGetConnectionW.Call(
		uintptr(unsafe.Pointer(local)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)))
	if r1 != 0 {
		return "", syscall.Errno(r1)
	}
	return strings.ReplaceAll(syscall.UTF16ToString(buffer), `\`, "/"), nil
}

func networkProvider(source string) (string, error) {
	remoteName := strings.ReplaceAll(source, "/", `\`)
	if !strings.HasPrefix(remoteName, `\\`) {
		remoteName = `\\` + strings.TrimLeft(remoteName, `\`)
	}
	remote, err := windows.UTF16PtrFromString(remoteName)
	if err != nil {
		return "", err
	}
	resource := netResource{Type: resourceTypeDisk, RemoteName: remote}
	buffer := make([]uintptr, 4096)
	size := uint32(len(buffer) * int(unsafe.Sizeof(buffer[0])))
	var system *uint16
	r1, _, _ := procWNetGetResourceInformationW.Call(
		uintptr(unsafe.Pointer(&resource)), uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&system)))
	if r1 != 0 {
		return "", syscall.Errno(r1)
	}
	resolved := (*netResource)(unsafe.Pointer(&buffer[0]))
	return windows.UTF16PtrToString(resolved.Provider), nil
}

func connectNetworkDrive(disk config.DiskConfig) error {
	if disk.User != "" || disk.Password != "" {
		return errors.New("Windows SMB credentials must not be supplied through YAML user/password")
	}
	remoteName := strings.ReplaceAll(disk.Device, "/", `\`)
	if !strings.HasPrefix(remoteName, `\\`) {
		remoteName = `\\` + strings.TrimLeft(remoteName, `\`)
	}
	localName := strings.TrimSuffix(disk.Target, `\`)
	if len(localName) != 2 || localName[1] != ':' {
		return fmt.Errorf("Windows SMB target must be a drive root such as F:\\: %s", disk.Target)
	}
	local, _ := syscall.UTF16PtrFromString(localName)
	remote, _ := syscall.UTF16PtrFromString(remoteName)
	resource := netResource{Type: resourceTypeDisk, LocalName: local, RemoteName: remote}
	var username, password *uint16
	var passwordBuffer []uint16
	if strings.EqualFold(disk.Credential, "windows-credential-manager") {
		var credentialUser string
		var err error
		credentialUser, passwordBuffer, err = readWindowsSMBCredential(disk.Device)
		if err != nil {
			return err
		}
		defer zeroUTF16(passwordBuffer)
		username, err = syscall.UTF16PtrFromString(credentialUser)
		if err != nil {
			return fmt.Errorf("decode Windows Credential Manager username: %w", err)
		}
		password = &passwordBuffer[0]
	}
	r1, _, _ := procWNetAddConnection2W.Call(
		uintptr(unsafe.Pointer(&resource)), uintptr(unsafe.Pointer(password)), uintptr(unsafe.Pointer(username)), 0)
	if r1 != 0 {
		return fmt.Errorf("connect SMB %s at %s: %w", disk.Device, disk.Target, syscall.Errno(r1))
	}
	return nil
}

func sameSMBSource(actual, expected string) bool {
	normalize := func(value string) string {
		value = strings.ReplaceAll(value, `\`, "/")
		return strings.TrimSuffix(strings.TrimPrefix(value, "//"), "/")
	}
	return strings.EqualFold(normalize(actual), normalize(expected))
}

func openProcess(pid int, access uint32) (uintptr, error) {
	handle, _, err := procOpenProcess.Call(uintptr(access), 0, uintptr(uint32(pid)))
	if handle == 0 {
		return 0, fmt.Errorf("open process %d: %w", pid, errnoError(err))
	}
	return handle, nil
}

func processImagePath(handle uintptr) (string, error) {
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	r1, _, err := procQueryFullProcessImageNameW.Call(
		handle, 0, uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)))
	if r1 == 0 {
		return "", errnoError(err)
	}
	return syscall.UTF16ToString(buffer[:size]), nil
}

func processTimes(handle uintptr) (time.Time, uint64, error) {
	var created, exited, kernel, user syscall.Filetime
	r1, _, err := procGetProcessTimes.Call(
		handle, uintptr(unsafe.Pointer(&created)), uintptr(unsafe.Pointer(&exited)),
		uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
	if r1 == 0 {
		return time.Time{}, 0, errnoError(err)
	}
	ticks := (uint64(created.HighDateTime) << 32) | uint64(created.LowDateTime)
	cpuTicks := (uint64(kernel.HighDateTime) << 32) | uint64(kernel.LowDateTime)
	cpuTicks += (uint64(user.HighDateTime) << 32) | uint64(user.LowDateTime)
	return time.Unix(0, (int64(ticks)-windowsToUnixEpoch100Nano)*100).UTC(), cpuTicks, nil
}

func commandLineArguments(commandLine string) ([]string, error) {
	if strings.TrimSpace(commandLine) == "" {
		return nil, errors.New("empty command line")
	}
	input, err := windows.UTF16PtrFromString(commandLine)
	if err != nil {
		return nil, err
	}
	var argc int32
	argv, err := windows.CommandLineToArgv(input, &argc)
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(argv)))
	if argc < 0 || argc > int32(len(argv)) {
		return nil, fmt.Errorf("CommandLineToArgvW returned unsupported argument count %d", argc)
	}
	arguments := make([]string, 0, argc)
	for _, argument := range argv[:argc] {
		arguments = append(arguments, windows.UTF16ToString(argument[:]))
	}
	return arguments, nil
}

func containsRequiredArguments(arguments, required []string) bool {
	for _, expected := range required {
		found := false
		for i, actual := range arguments {
			if strings.EqualFold(actual, expected) {
				found = true
				break
			}
			parts := strings.SplitN(expected, "=", 2)
			if len(parts) == 2 && strings.EqualFold(actual, parts[0]) && i+1 < len(arguments) && strings.EqualFold(arguments[i+1], parts[1]) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func sameWindowsPath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func countWindowsFFmpeg(rootPID int) int {
	entries, err := windowsProcesses()
	if err != nil {
		return 0
	}
	descendants := map[uint32]bool{uint32(rootPID): true}
	for changed := true; changed; {
		changed = false
		for _, entry := range entries {
			if descendants[entry.ParentProcessID] && !descendants[entry.ProcessID] {
				descendants[entry.ProcessID] = true
				changed = true
			}
		}
	}
	count := 0
	for _, entry := range entries {
		name := strings.ToLower(syscall.UTF16ToString(entry.ExeFile[:]))
		if entry.ProcessID != uint32(rootPID) && descendants[entry.ProcessID] && strings.Contains(name, "ffmpeg") {
			count++
		}
	}
	return count
}

func windowsProcesses() ([]processEntry32, error) {
	handle, _, err := procCreateToolhelp32Snapshot.Call(th32csSnapProcess, 0)
	if handle == ^uintptr(0) {
		return nil, errnoError(err)
	}
	defer procCloseHandle.Call(handle)
	entry := processEntry32{Size: uint32(unsafe.Sizeof(processEntry32{}))}
	r1, _, callErr := procProcess32FirstW.Call(handle, uintptr(unsafe.Pointer(&entry)))
	if r1 == 0 {
		return nil, errnoError(callErr)
	}
	var entries []processEntry32
	for {
		entries = append(entries, entry)
		entry = processEntry32{Size: uint32(unsafe.Sizeof(processEntry32{}))}
		r1, _, _ = procProcess32NextW.Call(handle, uintptr(unsafe.Pointer(&entry)))
		if r1 == 0 {
			break
		}
	}
	return entries, nil
}

func canonicalVolumeGUID(value string) string {
	return ensureTrailingBackslash(strings.TrimSpace(value))
}

func ensureTrailingBackslash(value string) string {
	if strings.HasSuffix(value, `\`) {
		return value
	}
	return value + `\`
}

func errnoError(err error) error {
	if err == nil || errors.Is(err, syscall.Errno(0)) {
		return errors.New("Windows API call failed")
	}
	return err
}
