package qemu

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/dropbox/godropbox/container/set"
	"github.com/dropbox/godropbox/errors"
	"github.com/pritunl/mongo-go-driver/bson"
	"github.com/pritunl/mongo-go-driver/bson/primitive"
	"github.com/pritunl/pritunl-cloud/block"
	"github.com/pritunl/pritunl-cloud/cloudinit"
	"github.com/pritunl/pritunl-cloud/data"
	"github.com/pritunl/pritunl-cloud/database"
	"github.com/pritunl/pritunl-cloud/disk"
	"github.com/pritunl/pritunl-cloud/errortypes"
	"github.com/pritunl/pritunl-cloud/event"
	"github.com/pritunl/pritunl-cloud/instance"
	"github.com/pritunl/pritunl-cloud/interfaces"
	"github.com/pritunl/pritunl-cloud/iptables"
	"github.com/pritunl/pritunl-cloud/node"
	"github.com/pritunl/pritunl-cloud/paths"
	"github.com/pritunl/pritunl-cloud/qms"
	"github.com/pritunl/pritunl-cloud/settings"
	"github.com/pritunl/pritunl-cloud/store"
	"github.com/pritunl/pritunl-cloud/systemd"
	"github.com/pritunl/pritunl-cloud/utils"
	"github.com/pritunl/pritunl-cloud/vm"
	"github.com/pritunl/pritunl-cloud/vpc"
	"github.com/pritunl/pritunl-cloud/zone"
)

var (
	serviceReg = regexp.MustCompile("pritunl_cloud_([a-z0-9]+).service")
)

type InfoCache struct {
	Timestamp time.Time
	Virt      *vm.VirtualMachine
}

func GetVmInfo(vmId primitive.ObjectID, getDisks, force bool) (
	virt *vm.VirtualMachine, err error) {

	refreshRate := time.Duration(
		settings.Hypervisor.RefreshRate) * time.Second

	virtStore, ok := store.GetVirt(vmId)
	if !ok {
		unitPath := paths.GetUnitPath(vmId)

		unitData, e := ioutil.ReadFile(unitPath)
		if e != nil {
			// TODO if err.(*os.PathError).Error == os.ErrNotExist {
			err = &errortypes.ReadError{
				errors.Wrap(e, "qemu: Failed to read service"),
			}
			return
		}

		virt = &vm.VirtualMachine{}
		for _, line := range strings.Split(string(unitData), "\n") {
			if !strings.HasPrefix(line, "PritunlData=") {
				continue
			}

			lineSpl := strings.SplitN(line, "=", 2)
			if len(lineSpl) != 2 || len(lineSpl[1]) < 6 {
				continue
			}

			err = json.Unmarshal([]byte(lineSpl[1]), virt)
			if err != nil {
				err = &errortypes.ParseError{
					errors.Wrap(err, "qemu: Failed to parse service data"),
				}
				return
			}

			break
		}

		if virt.Id.IsZero() {
			virt = nil
			return
		}

		_ = UpdateVmState(virt)
	} else {
		virt = &virtStore.Virt

		if force || virt.State != vm.Running ||
			time.Since(virtStore.Timestamp) > 3*time.Second {

			_ = UpdateVmState(virt)
		}
	}

	if virt.State == vm.Running && getDisks {
		disksStore, ok := store.GetDisks(vmId)
		if !ok || time.Since(disksStore.Timestamp) > refreshRate {
			for i := 0; i < 20; i++ {
				if virt.State == vm.Running {
					disks, e := qms.GetDisks(vmId)
					if e != nil {
						if i < 19 {
							time.Sleep(100 * time.Millisecond)
							_ = UpdateVmState(virt)
							continue
						}
						err = e
						return
					}
					virt.Disks = disks

					store.SetDisks(vmId, disks)
				}

				break
			}
		} else {
			disks := []*vm.Disk{}
			for _, dsk := range disksStore.Disks {
				disks = append(disks, &dsk)
			}
			virt.Disks = disks
		}
	}

	addrStore, ok := store.GetAddress(virt.Id)
	if !ok {
		addr := ""
		addr6 := ""

		namespace := vm.GetNamespace(virt.Id, 0)
		ifaceExternal := vm.GetIfaceExternal(virt.Id, 0)

		externalNetwork := true
		if node.Self.NetworkMode == node.Internal {
			externalNetwork = false
		}

		if externalNetwork {
			ipData, e := utils.ExecCombinedOutputLogged(
				[]string{
					"No such file or directory",
					"does not exist",
					"setting the network namespace",
				},
				"ip", "netns", "exec", namespace,
				"ip", "-f", "inet", "-o", "addr",
				"show", "dev", ifaceExternal,
			)
			if e != nil {
				err = e
				return
			}

			fields := strings.Fields(ipData)
			if len(fields) > 3 {
				ipAddr := net.ParseIP(strings.Split(fields[3], "/")[0])
				if ipAddr != nil && len(ipAddr) > 0 &&
					len(virt.NetworkAdapters) > 0 {

					addr = ipAddr.String()
				}
			}

			ipData, e = utils.ExecCombinedOutputLogged(
				[]string{
					"No such file or directory",
					"does not exist",
					"setting the network namespace",
				},
				"ip", "netns", "exec", namespace,
				"ip", "-f", "inet6", "-o", "addr",
				"show", "dev", ifaceExternal,
			)
			if e != nil {
				err = e
				return
			}

			for _, line := range strings.Split(ipData, "\n") {
				if !strings.Contains(line, "global") {
					continue
				}

				fields = strings.Fields(ipData)
				if len(fields) > 3 {
					ipAddr := net.ParseIP(strings.Split(fields[3], "/")[0])
					if ipAddr != nil && len(ipAddr) > 0 {
						addr6 = ipAddr.String()
					}
				}

				break
			}

			if len(virt.NetworkAdapters) > 0 {
				virt.NetworkAdapters[0].IpAddress = addr
				virt.NetworkAdapters[0].IpAddress6 = addr6
			}
			store.SetAddress(virt.Id, addr, addr6)
		}
	} else {
		if len(virt.NetworkAdapters) > 0 {
			virt.NetworkAdapters[0].IpAddress = addrStore.Addr
			virt.NetworkAdapters[0].IpAddress6 = addrStore.Addr6
		}
	}

	return
}

func UpdateVmState(virt *vm.VirtualMachine) (err error) {
	unitName := paths.GetUnitName(virt.Id)
	state, err := systemd.GetState(unitName)
	if err != nil {
		return
	}

	switch state {
	case "active":
		virt.State = vm.Running
		break
	case "deactivating":
		virt.State = vm.Running
		break
	case "inactive":
		virt.State = vm.Stopped
		break
	case "failed":
		virt.State = vm.Failed
		break
	case "unknown":
		virt.State = vm.Stopped
		break
	default:
		logrus.WithFields(logrus.Fields{
			"id":    virt.Id.Hex(),
			"state": state,
		}).Info("qemu: Unknown virtual machine state")
		virt.State = vm.Failed
		break
	}

	store.SetVirt(virt.Id, virt)

	return
}

func GetVms(db *database.Database,
	instMap map[primitive.ObjectID]*instance.Instance) (
	virts []*vm.VirtualMachine, err error) {

	systemdPath := settings.Hypervisor.SystemdPath
	virts = []*vm.VirtualMachine{}

	items, err := ioutil.ReadDir(systemdPath)
	if err != nil {
		err = &errortypes.ReadError{
			errors.Wrap(err, "qemu: Failed to read systemd directory"),
		}
		return
	}

	units := []string{}
	for _, item := range items {
		if strings.HasPrefix(item.Name(), "pritunl_cloud") {
			units = append(units, item.Name())
		}
	}

	waiter := sync.WaitGroup{}
	virtsLock := sync.Mutex{}

	for _, unit := range units {
		match := serviceReg.FindStringSubmatch(unit)
		if match == nil || len(match) != 2 {
			continue
		}

		vmId, err := primitive.ObjectIDFromHex(match[1])
		if err != nil {
			continue
		}

		waiter.Add(1)
		go func() {
			defer waiter.Done()

			virt, e := GetVmInfo(vmId, true, false)
			if e != nil {
				err = e
				return
			}

			if virt != nil {
				inst := instMap[vmId]
				if inst != nil && inst.VmState == vm.Running &&
					(virt.State == vm.Stopped || virt.State == vm.Failed) {

					inst.State = instance.Cleanup
					e = virt.CommitState(db, instance.Cleanup)
				} else {
					e = virt.Commit(db)
				}
				if e != nil {
					logrus.WithFields(logrus.Fields{
						"error": e,
					}).Error("qemu: Failed to commit VM state")
				}

				virtsLock.Lock()
				virts = append(virts, virt)
				virtsLock.Unlock()
			}
		}()
	}

	waiter.Wait()

	return
}

func Wait(db *database.Database, virt *vm.VirtualMachine) (err error) {
	unitName := paths.GetUnitName(virt.Id)

	for i := 0; i < settings.Hypervisor.StartTimeout; i++ {
		err = UpdateVmState(virt)
		if err != nil {
			return
		}

		if virt.State == vm.Running {
			break
		}

		time.Sleep(1 * time.Second)
	}

	if virt.State != vm.Running {
		err = systemd.Stop(unitName)
		if err != nil {
			return
		}

		err = &errortypes.TimeoutError{
			errors.New("qemu: Power on timeout"),
		}

		return
	}

	return
}

func NetworkConf(db *database.Database,
	virt *vm.VirtualMachine) (err error) {

	ifaceNames := set.NewSet()

	if len(virt.NetworkAdapters) == 0 {
		err = &errortypes.NotFoundError{
			errors.New("qemu: Missing network interfaces"),
		}
		return
	}

	for i := range virt.NetworkAdapters {
		ifaceNames.Add(vm.GetIface(virt.Id, i))
	}

	for i := 0; i < 100; i++ {
		ifaces, e := net.Interfaces()
		if e != nil {
			err = &errortypes.ReadError{
				errors.Wrap(e, "qemu: Failed to get network interfaces"),
			}
			return
		}

		for _, iface := range ifaces {
			if ifaceNames.Contains(iface.Name) {
				ifaceNames.Remove(iface.Name)
			}
		}

		if ifaceNames.Len() == 0 {
			break
		}

		time.Sleep(250 * time.Millisecond)
	}

	if ifaceNames.Len() != 0 {
		err = &errortypes.ReadError{
			errors.New("qemu: Failed to find network interfaces"),
		}
		return
	}

	zne, err := zone.Get(db, node.Self.Zone)
	if err != nil {
		return
	}

	vxlan := false
	if zne.NetworkMode == zone.VxlanVlan {
		vxlan = true
	}

	nodeNetworkMode := node.Self.NetworkMode
	jumboFrames := node.Self.JumboFrames
	iface := vm.GetIface(virt.Id, 0)
	ifaceExternalVirt := vm.GetIfaceVirt(virt.Id, 0)
	ifaceInternalVirt := vm.GetIfaceVirt(virt.Id, 1)
	ifaceHostVirt := vm.GetIfaceVirt(virt.Id, 2)
	ifaceExternal := vm.GetIfaceExternal(virt.Id, 0)
	ifaceInternal := vm.GetIfaceInternal(virt.Id, 0)
	ifaceHost := vm.GetIfaceHost(virt.Id, 0)
	ifaceVlan := vm.GetIfaceVlan(virt.Id, 0)
	namespace := vm.GetNamespace(virt.Id, 0)
	pidPath := fmt.Sprintf("/var/run/dhclient-%s.pid", ifaceExternal)
	leasePath := paths.GetLeasePath(virt.Id)
	adapter := virt.NetworkAdapters[0]

	externalNetwork := true
	if virt.NoPublicAddress || nodeNetworkMode == node.Internal {
		externalNetwork = false
	}

	hostNetwork := false
	if !virt.NoHostAddress && !node.Self.HostBlock.IsZero() {
		hostNetwork = true
	}

	updateMtuInternal := ""
	updateMtuExternal := ""
	updateMtuInstance := ""
	updateMtuHost := ""
	if jumboFrames || vxlan {
		mtuSize := 0
		if jumboFrames {
			mtuSize = settings.Hypervisor.JumboMtu
		} else {
			mtuSize = settings.Hypervisor.NormalMtu
		}

		updateMtuExternal = strconv.Itoa(mtuSize)
		updateMtuHost = strconv.Itoa(mtuSize)

		if vxlan {
			mtuSize -= 50
		}

		updateMtuInternal = strconv.Itoa(mtuSize)

		if vxlan {
			mtuSize -= 4
		}

		updateMtuInstance = strconv.Itoa(mtuSize)
	}

	err = utils.ExistsMkdir(paths.GetLeasesPath(), 0755)
	if err != nil {
		return
	}

	vc, err := vpc.Get(db, adapter.VpcId)
	if err != nil {
		return
	}

	vcNet, err := vc.GetNetwork()
	if err != nil {
		return
	}

	addr, err := vc.GetIp(db, vpc.Instance, virt.Id)
	if err != nil {
		return
	}

	gatewayAddr, err := vc.GetIp(db, vpc.Gateway, virt.Id)
	if err != nil {
		return
	}

	addr6 := vc.GetIp6(addr)
	if err != nil {
		return
	}

	gatewayAddr6 := vc.GetIp6(gatewayAddr)
	if err != nil {
		return
	}

	cidr, _ := vcNet.Mask.Size()
	gatewayCidr := fmt.Sprintf("%s/%d", gatewayAddr.String(), cidr)

	_, err = utils.ExecCombinedOutputLogged(
		[]string{"File exists"},
		"ip", "netns",
		"add", namespace,
	)
	if err != nil {
		return
	}

	_, _ = utils.ExecCombinedOutput(
		"", "ip", "link", "set", ifaceExternalVirt, "down")
	_, _ = utils.ExecCombinedOutput(
		"", "ip", "link", "del", ifaceExternalVirt)
	_, _ = utils.ExecCombinedOutput(
		"", "ip", "link", "set", ifaceInternalVirt, "down")
	_, _ = utils.ExecCombinedOutput(
		"", "ip", "link", "del", ifaceInternalVirt)
	_, _ = utils.ExecCombinedOutput(
		"", "ip", "link", "set", ifaceHostVirt, "down")
	_, _ = utils.ExecCombinedOutput(
		"", "ip", "link", "del", ifaceHostVirt)

	interfaces.RemoveVirtIface(ifaceExternalVirt)
	interfaces.RemoveVirtIface(ifaceInternalVirt)

	macAddrExternal := vm.GetMacAddrExternal(virt.Id, vc.Id)
	macAddrInternal := vm.GetMacAddrInternal(virt.Id, vc.Id)
	macAddrHost := vm.GetMacAddrHost(virt.Id, vc.Id)

	if externalNetwork {
		_, err = utils.ExecCombinedOutputLogged(
			nil,
			"ip", "link",
			"add", ifaceExternalVirt,
			"type", "veth",
			"peer", "name", ifaceExternal,
			"addr", macAddrExternal,
		)
		if err != nil {
			return
		}
	}

	_, err = utils.ExecCombinedOutputLogged(
		nil,
		"ip", "link",
		"add", ifaceInternalVirt,
		"type", "veth",
		"peer", "name", ifaceInternal,
		"addr", macAddrInternal,
	)
	if err != nil {
		return
	}

	if hostNetwork {
		_, err = utils.ExecCombinedOutputLogged(
			nil,
			"ip", "link",
			"add", ifaceHostVirt,
			"type", "veth",
			"peer", "name", ifaceHost,
			"addr", macAddrHost,
		)
		if err != nil {
			return
		}
	}

	if externalNetwork && updateMtuExternal != "" {
		_, err = utils.ExecCombinedOutputLogged(
			nil,
			"ip", "link",
			"set", "dev", ifaceExternalVirt,
			"mtu", updateMtuExternal,
		)
		if err != nil {
			return
		}

		_, err = utils.ExecCombinedOutputLogged(
			nil,
			"ip", "link",
			"set", "dev", ifaceExternal,
			"mtu", updateMtuExternal,
		)
		if err != nil {
			return
		}
	}

	if updateMtuInternal != "" {
		_, err = utils.ExecCombinedOutputLogged(
			nil,
			"ip", "link",
			"set", "dev", ifaceInternalVirt,
			"mtu", updateMtuInternal,
		)
		if err != nil {
			return
		}

		_, err = utils.ExecCombinedOutputLogged(
			nil,
			"ip", "link",
			"set", "dev", ifaceInternal,
			"mtu", updateMtuInternal,
		)
		if err != nil {
			return
		}
	}

	if hostNetwork && updateMtuHost != "" {
		_, err = utils.ExecCombinedOutputLogged(
			nil,
			"ip", "link",
			"set", "dev", ifaceHostVirt,
			"mtu", updateMtuHost,
		)
		if err != nil {
			return
		}

		_, err = utils.ExecCombinedOutputLogged(
			nil,
			"ip", "link",
			"set", "dev", ifaceHost,
			"mtu", updateMtuHost,
		)
		if err != nil {
			return
		}
	}

	if externalNetwork {
		_, err = utils.ExecCombinedOutputLogged(
			nil,
			"ip", "link",
			"set", "dev", ifaceExternalVirt, "up",
		)
		if err != nil {
			return
		}
	}

	_, err = utils.ExecCombinedOutputLogged(
		nil,
		"ip", "link",
		"set", "dev", ifaceInternalVirt, "up",
	)
	if err != nil {
		return
	}

	if hostNetwork {
		_, err = utils.ExecCombinedOutputLogged(
			nil,
			"ip", "link",
			"set", "dev", ifaceHostVirt, "up",
		)
		if err != nil {
			return
		}
	}

	internalIface := interfaces.GetInternal(ifaceInternalVirt, vxlan)
	if internalIface == "" {
		err = &errortypes.NotFoundError{
			errors.New("qemu: Failed to get internal interface"),
		}
		return
	}

	var externalIface string
	var blck *block.Block
	var staticAddr net.IP

	if externalNetwork {
		if nodeNetworkMode == node.Static {
			blck, staticAddr, externalIface, err = node.Self.GetStaticAddr(
				db, virt.Id)
			if err != nil {
				return
			}
		} else if nodeNetworkMode == node.Internal {

		} else {
			externalIface = interfaces.GetExternal(ifaceExternalVirt)
		}
		if externalIface == "" {
			err = &errortypes.NotFoundError{
				errors.New("qemu: Failed to get external interface"),
			}
			return
		}
	}

	hostIface := settings.Hypervisor.HostNetworkName
	var hostBlck *block.Block
	var hostStaticAddr net.IP
	if hostNetwork {
		hostBlck, hostStaticAddr, err = node.Self.GetStaticHostAddr(
			db, virt.Id)
		if err != nil {
			return
		}
	}

	if externalNetwork {
		_, err = utils.ExecCombinedOutputLogged(
			nil, "sysctl", "-w",
			fmt.Sprintf("net.ipv6.conf.%s.accept_ra=2", externalIface),
		)
		if err != nil {
			return
		}
	}
	if !externalNetwork || internalIface != externalIface {
		_, err = utils.ExecCombinedOutputLogged(
			nil, "sysctl", "-w",
			fmt.Sprintf("net.ipv6.conf.%s.accept_ra=2", internalIface),
		)
		if err != nil {
			return
		}
	}

	if externalNetwork {
		_, err = utils.ExecCombinedOutputLogged(
			[]string{"already a member of a bridge"},
			"brctl", "addif", externalIface, ifaceExternalVirt)
		if err != nil {
			return
		}
	}

	_, err = utils.ExecCombinedOutputLogged(
		[]string{"already a member of a bridge"},
		"brctl", "addif", internalIface, ifaceInternalVirt)
	if err != nil {
		return
	}

	if hostNetwork {
		_, err = utils.ExecCombinedOutputLogged(
			[]string{"already a member of a bridge"},
			"brctl", "addif", hostIface, ifaceHostVirt)
		if err != nil {
			return
		}
	}

	if externalNetwork {
		_, err = utils.ExecCombinedOutputLogged(
			[]string{"File exists"},
			"ip", "link",
			"set", "dev", ifaceExternal,
			"netns", namespace,
		)
		if err != nil {
			return
		}
	}

	_, err = utils.ExecCombinedOutputLogged(
		[]string{"File exists"},
		"ip", "link",
		"set", "dev", ifaceInternal,
		"netns", namespace,
	)
	if err != nil {
		return
	}

	if hostNetwork {
		_, err = utils.ExecCombinedOutputLogged(
			[]string{"File exists"},
			"ip", "link",
			"set", "dev", ifaceHost,
			"netns", namespace,
		)
		if err != nil {
			return
		}
	}

	_, err = utils.ExecCombinedOutputLogged(
		nil,
		"ip", "netns", "exec", namespace,
		"sysctl", "-w", "net.ipv6.conf.all.accept_ra=0",
	)
	if err != nil {
		return
	}

	_, err = utils.ExecCombinedOutputLogged(
		nil,
		"ip", "netns", "exec", namespace,
		"sysctl", "-w", "net.ipv6.conf.default.accept_ra=0",
	)
	if err != nil {
		return
	}

	if externalNetwork {
		_, err = utils.ExecCombinedOutputLogged(
			nil,
			"ip", "netns", "exec", namespace,
			"sysctl", "-w",
			fmt.Sprintf("net.ipv6.conf.%s.accept_ra=2", ifaceExternal),
		)
		if err != nil {
			return
		}
	}

	if externalNetwork {
		iptables.Lock()
		_, err = utils.ExecCombinedOutputLogged(
			nil,
			"ip", "netns", "exec", namespace,
			"iptables",
			"-I", "FORWARD", "1",
			"!", "-d", addr.String()+"/32",
			"-i", ifaceExternal,
			"-j", "DROP",
		)
		iptables.Unlock()
		if err != nil {
			return
		}

		iptables.Lock()
		_, err = utils.ExecCombinedOutputLogged(
			nil,
			"ip", "netns", "exec", namespace,
			"ip6tables",
			"-I", "FORWARD", "1",
			"!", "-d", addr6.String()+"/128",
			"-i", ifaceExternal,
			"-j", "DROP",
		)
		iptables.Unlock()
		if err != nil {
			return
		}
	}

	if hostNetwork {
		iptables.Lock()
		_, err = utils.ExecCombinedOutputLogged(
			nil,
			"ip", "netns", "exec", namespace,
			"iptables",
			"-I", "FORWARD", "1",
			"!", "-d", addr.String()+"/32",
			"-i", ifaceHost,
			"-j", "DROP",
		)
		iptables.Unlock()
		if err != nil {
			return
		}
	}

	_, err = utils.ExecCombinedOutputLogged(
		nil,
		"ip", "netns", "exec", namespace,
		"sysctl", "-w", "net.ipv4.ip_forward=1",
	)
	if err != nil {
		return
	}

	_, err = utils.ExecCombinedOutputLogged(
		nil,
		"ip", "netns", "exec", namespace,
		"sysctl", "-w", "net.ipv6.conf.all.forwarding=1",
	)
	if err != nil {
		return
	}

	_, err = utils.ExecCombinedOutputLogged(
		[]string{"File exists"},
		"ip", "link",
		"set", "dev", iface,
		"netns", namespace,
	)
	if err != nil {
		return
	}

	_, err = utils.ExecCombinedOutputLogged(
		nil,
		"ip", "netns", "exec", namespace,
		"ip", "link",
		"set", "dev", "lo", "up",
	)
	if err != nil {
		return
	}

	if externalNetwork {
		_, err = utils.ExecCombinedOutputLogged(
			nil,
			"ip", "netns", "exec", namespace,
			"ip", "link",
			"set", "dev", ifaceExternal, "up",
		)
		if err != nil {
			return
		}
	}

	_, err = utils.ExecCombinedOutputLogged(
		nil,
		"ip", "netns", "exec", namespace,
		"ip", "link",
		"set", "dev", ifaceInternal, "up",
	)
	if err != nil {
		return
	}

	if hostNetwork {
		_, err = utils.ExecCombinedOutputLogged(
			nil,
			"ip", "netns", "exec", namespace,
			"ip", "link",
			"set", "dev", ifaceHost, "up",
		)
		if err != nil {
			return
		}
	}

	if updateMtuInstance != "" {
		_, err = utils.ExecCombinedOutputLogged(
			nil,
			"ip", "netns", "exec", namespace,
			"ip", "link",
			"set", "dev", iface,
			"mtu", updateMtuInstance,
		)
		if err != nil {
			return
		}
	}

	_, err = utils.ExecCombinedOutputLogged(
		nil,
		"ip", "netns", "exec", namespace,
		"ip", "link",
		"set", "dev", iface, "up",
	)
	if err != nil {
		return
	}

	_, err = utils.ExecCombinedOutputLogged(
		[]string{"File exists"},
		"ip", "netns", "exec", namespace,
		"ip", "link",
		"add", "link", ifaceInternal,
		"name", ifaceVlan,
		"type", "vlan",
		"id", strconv.Itoa(vc.VpcId),
	)
	if err != nil {
		return
	}

	if updateMtuInternal != "" {
		_, err = utils.ExecCombinedOutputLogged(
			nil,
			"ip", "netns", "exec", namespace,
			"ip", "link",
			"set", "dev", ifaceVlan,
			"mtu", updateMtuInternal,
		)
		if err != nil {
			return
		}
	}

	_, err = utils.ExecCombinedOutputLogged(
		nil,
		"ip", "netns", "exec", namespace,
		"ip", "link",
		"set", "dev", ifaceVlan, "up",
	)
	if err != nil {
		return
	}

	_, err = utils.ExecCombinedOutputLogged(
		[]string{"already exists"},
		"ip", "netns", "exec", namespace,
		"brctl", "addbr", "br0",
	)
	if err != nil {
		return
	}

	//_, err = utils.ExecCombinedOutputLogged(
	//	nil,
	//	"ip", "netns", "exec", namespace,
	//	"brctl", "stp", "br0", "on",
	//)
	//if err != nil {
	//	return
	//}

	_, err = utils.ExecCombinedOutputLogged(
		[]string{"already a member of a bridge"},
		"ip", "netns", "exec", namespace,
		"brctl", "addif", "br0", ifaceVlan,
	)
	if err != nil {
		return
	}

	_, err = utils.ExecCombinedOutputLogged(
		[]string{"already a member of a bridge"},
		"ip", "netns", "exec", namespace,
		"brctl", "addif", "br0", iface,
	)
	if err != nil {
		return
	}

	_, err = utils.ExecCombinedOutputLogged(
		[]string{"File exists"},
		"ip", "netns", "exec", namespace,
		"ip", "addr",
		"add", gatewayCidr,
		"dev", "br0",
	)
	if err != nil {
		return
	}

	_, err = utils.ExecCombinedOutputLogged(
		[]string{"File exists"},
		"ip", "netns", "exec", namespace,
		"ip", "-6", "addr",
		"add", gatewayAddr6.String()+"/64",
		"dev", "br0",
	)
	if err != nil {
		return
	}

	_, err = utils.ExecCombinedOutputLogged(
		nil,
		"ip", "netns", "exec", namespace,
		"ip", "link",
		"set", "dev", "br0", "up",
	)
	if err != nil {
		return
	}

	_ = networkStopDhClient(db, virt)

	if externalNetwork {
		if nodeNetworkMode == node.Static {
			staticGateway := blck.GetGateway()
			staticMask := blck.GetMask()
			if staticGateway == nil || staticMask == nil {
				err = &errortypes.ParseError{
					errors.New("qemu: Invalid block gateway cidr"),
				}
				return
			}

			staticSize, _ := staticMask.Size()
			staticCidr := fmt.Sprintf(
				"%s/%d", staticAddr.String(), staticSize)

			_, err = utils.ExecCombinedOutputLogged(
				[]string{"File exists"},
				"ip", "netns", "exec", namespace,
				"ip", "addr",
				"add", staticCidr,
				"dev", ifaceExternal,
			)
			if err != nil {
				return
			}

			_, err = utils.ExecCombinedOutputLogged(
				[]string{"File exists"},
				"ip", "netns", "exec", namespace,
				"ip", "route",
				"add", "default",
				"via", staticGateway.String(),
			)
			if err != nil {
				return
			}
		} else {
			_, err = utils.ExecCombinedOutputLogged(
				nil,
				"ip", "netns", "exec", namespace,
				"dhclient",
				"-pf", pidPath,
				"-lf", leasePath,
				ifaceExternal,
			)
			if err != nil {
				return
			}
		}
	}

	if hostNetwork {
		hostStaticGateway := hostBlck.GetGateway()
		hostStaticMask := hostBlck.GetMask()
		if hostStaticGateway == nil || hostStaticMask == nil {
			err = &errortypes.ParseError{
				errors.New("qemu: Invalid block gateway cidr"),
			}
			return
		}

		hostStaticSize, _ := hostStaticMask.Size()
		hostStaticCidr := fmt.Sprintf(
			"%s/%d", hostStaticAddr.String(), hostStaticSize)

		_, err = utils.ExecCombinedOutputLogged(
			[]string{"File exists"},
			"ip", "netns", "exec", namespace,
			"ip", "addr",
			"add", hostStaticCidr,
			"dev", ifaceHost,
		)
		if err != nil {
			return
		}

		if !externalNetwork {
			_, err = utils.ExecCombinedOutputLogged(
				[]string{"File exists"},
				"ip", "netns", "exec", namespace,
				"ip", "route",
				"add", "default",
				"via", hostStaticGateway.String(),
			)
			if err != nil {
				return
			}
		}

	}

	time.Sleep(2 * time.Second)
	start := time.Now()

	pubAddr := ""
	pubAddr6 := ""
	if externalNetwork {
		for i := 0; i < 60; i++ {
			ipData, e := utils.ExecCombinedOutputLogged(
				[]string{
					"No such file or directory",
					"does not exist",
				},
				"ip", "netns", "exec", namespace,
				"ip", "-f", "inet", "-o", "addr",
				"show", "dev", ifaceExternal,
			)
			if e != nil {
				err = e
				return
			}

			fields := strings.Fields(ipData)
			if len(fields) > 3 {
				ipAddr := net.ParseIP(strings.Split(fields[3], "/")[0])
				if ipAddr != nil && len(ipAddr) > 0 &&
					len(virt.NetworkAdapters) > 0 {

					pubAddr = ipAddr.String()
				}
			}

			ipData, e = utils.ExecCombinedOutputLogged(
				[]string{
					"No such file or directory",
					"does not exist",
				},
				"ip", "netns", "exec", namespace,
				"ip", "-f", "inet6", "-o", "addr",
				"show", "dev", ifaceExternal,
			)
			if e != nil {
				err = e
				return
			}

			for _, line := range strings.Split(ipData, "\n") {
				if !strings.Contains(line, "global") {
					continue
				}

				fields = strings.Fields(ipData)
				if len(fields) > 3 {
					ipAddr := net.ParseIP(strings.Split(fields[3], "/")[0])
					if ipAddr != nil && len(ipAddr) > 0 &&
						len(virt.NetworkAdapters) > 0 {

						pubAddr6 = ipAddr.String()
					}
				}

				break
			}

			if pubAddr != "" && (pubAddr6 != "" ||
				time.Since(start) > 8*time.Second) {

				break
			}

			time.Sleep(250 * time.Millisecond)
		}

		if pubAddr == "" {
			err = &errortypes.NetworkError{
				errors.New("qemu: Instance missing IPv4 address"),
			}
			return
		}

		iptables.Lock()
		_, err = utils.ExecCombinedOutputLogged(
			nil,
			"ip", "netns", "exec", namespace,
			"iptables", "-t", "nat",
			"-A", "POSTROUTING",
			"-s", addr.String()+"/32",
			"-o", ifaceExternal,
			"-j", "MASQUERADE",
		)
		iptables.Unlock()
		if err != nil {
			return
		}

		iptables.Lock()
		_, err = utils.ExecCombinedOutputLogged(
			nil,
			"ip", "netns", "exec", namespace,
			"iptables", "-t", "nat",
			"-A", "PREROUTING",
			"-d", pubAddr,
			"-j", "DNAT",
			"--to-destination", addr.String(),
		)
		iptables.Unlock()
		if err != nil {
			return
		}

		if pubAddr6 != "" {
			iptables.Lock()
			_, err = utils.ExecCombinedOutputLogged(
				nil,
				"ip", "netns", "exec", namespace,
				"ip6tables", "-t", "nat",
				"-A", "POSTROUTING",
				"-s", addr6.String()+"/32",
				"-o", ifaceExternal,
				"-j", "MASQUERADE",
			)
			iptables.Unlock()
			if err != nil {
				return
			}

			iptables.Lock()
			_, err = utils.ExecCombinedOutputLogged(
				nil,
				"ip", "netns", "exec", namespace,
				"ip6tables", "-t", "nat",
				"-A", "PREROUTING",
				"-d", pubAddr6,
				"-j", "DNAT",
				"--to-destination", addr6.String(),
			)
			iptables.Unlock()
			if err != nil {
				return
			}
		} else {
			logrus.WithFields(logrus.Fields{
				"instance_id":   virt.Id.Hex(),
				"net_namespace": namespace,
			}).Warning("qemu: Instance missing IPv6 address")
		}
	}

	if hostNetwork {
		iptables.Lock()
		_, err = utils.ExecCombinedOutputLogged(
			nil,
			"ip", "netns", "exec", namespace,
			"iptables", "-t", "nat",
			"-A", "POSTROUTING",
			"-s", addr.String()+"/32",
			"-o", ifaceHost,
			"-j", "MASQUERADE",
		)
		iptables.Unlock()
		if err != nil {
			return
		}

		iptables.Lock()
		_, err = utils.ExecCombinedOutputLogged(
			nil,
			"ip", "netns", "exec", namespace,
			"iptables", "-t", "nat",
			"-A", "PREROUTING",
			"-d", hostStaticAddr.String(),
			"-j", "DNAT",
			"--to-destination", addr.String(),
		)
		iptables.Unlock()
		if err != nil {
			return
		}
	}

	store.RemAddress(virt.Id)
	store.RemRoutes(virt.Id)

	hostIps := []string{}
	if hostStaticAddr != nil {
		hostIps = append(hostIps, hostStaticAddr.String())
	}

	coll := db.Instances()
	err = coll.UpdateId(virt.Id, &bson.M{
		"$set": &bson.M{
			"private_ips":  []string{addr.String()},
			"private_ips6": []string{addr6.String()},
			"host_ips":     hostIps,
		},
	})
	if err != nil {
		err = database.ParseError(err)
		if _, ok := err.(*database.NotFoundError); ok {
			err = nil
		} else {
			return
		}
	}

	return
}

func networkStopDhClient(db *database.Database,
	virt *vm.VirtualMachine) (err error) {

	if len(virt.NetworkAdapters) == 0 {
		err = &errortypes.NotFoundError{
			errors.New("qemu: Missing network interfaces"),
		}
		return
	}

	ifaceExternal := vm.GetIfaceExternal(virt.Id, 0)
	pidPath := fmt.Sprintf("/var/run/dhclient-%s.pid", ifaceExternal)

	pid := ""
	pidData, _ := ioutil.ReadFile(pidPath)
	if pidData != nil {
		pid = strings.TrimSpace(string(pidData))
	}

	if pid != "" {
		_, _ = utils.ExecCombinedOutput("", "kill", pid)
	}

	_ = utils.RemoveAll(pidPath)

	return
}

func NetworkConfClear(db *database.Database,
	virt *vm.VirtualMachine) (err error) {

	if len(virt.NetworkAdapters) == 0 {
		err = &errortypes.NotFoundError{
			errors.New("qemu: Missing network interfaces"),
		}
		return
	}

	err = networkStopDhClient(db, virt)
	if err != nil {
		return
	}

	ifaceExternalVirt := vm.GetIfaceVirt(virt.Id, 0)
	ifaceInternalVirt := vm.GetIfaceVirt(virt.Id, 1)
	ifaceHostVirt := vm.GetIfaceVirt(virt.Id, 2)

	_, _ = utils.ExecCombinedOutput(
		"", "ip", "link", "set", ifaceExternalVirt, "down")
	_, _ = utils.ExecCombinedOutput(
		"", "ip", "link", "del", ifaceExternalVirt)
	_, _ = utils.ExecCombinedOutput(
		"", "ip", "link", "set", ifaceInternalVirt, "down")
	_, _ = utils.ExecCombinedOutput(
		"", "ip", "link", "del", ifaceInternalVirt)
	_, _ = utils.ExecCombinedOutput(
		"", "ip", "link", "set", ifaceHostVirt, "down")
	_, _ = utils.ExecCombinedOutput(
		"", "ip", "link", "del", ifaceHostVirt)

	interfaces.RemoveVirtIface(ifaceExternalVirt)
	interfaces.RemoveVirtIface(ifaceInternalVirt)

	store.RemAddress(virt.Id)
	store.RemRoutes(virt.Id)

	return
}

func writeService(virt *vm.VirtualMachine) (err error) {
	unitPath := paths.GetUnitPath(virt.Id)

	qm, err := NewQemu(virt)
	if err != nil {
		return
	}

	output, err := qm.Marshal()
	if err != nil {
		return
	}

	err = utils.CreateWrite(unitPath, output, 0644)
	if err != nil {
		return
	}

	err = systemd.Reload()
	if err != nil {
		return
	}

	return
}

func Create(db *database.Database, inst *instance.Instance,
	virt *vm.VirtualMachine) (err error) {

	vmPath := paths.GetVmPath(virt.Id)
	unitName := paths.GetUnitName(virt.Id)

	logrus.WithFields(logrus.Fields{
		"id": virt.Id.Hex(),
	}).Info("qemu: Creating virtual machine")

	virt.State = vm.Provisioning
	err = virt.Commit(db)
	if err != nil {
		return
	}

	err = utils.ExistsMkdir(settings.Hypervisor.LibPath, 0755)
	if err != nil {
		return
	}

	err = utils.ExistsMkdir(vmPath, 0755)
	if err != nil {
		return
	}

	dsk, err := disk.GetInstanceIndex(db, inst.Id, "0")
	if err != nil {
		if _, ok := err.(*database.NotFoundError); ok {
			dsk = nil
			err = nil
		} else {
			return
		}
	}

	if dsk == nil {
		dsk = &disk.Disk{
			Id:               primitive.NewObjectID(),
			Name:             inst.Name,
			State:            disk.Available,
			Node:             node.Self.Id,
			Organization:     inst.Organization,
			Instance:         inst.Id,
			SourceInstance:   inst.Id,
			Image:            virt.Image,
			Backing:          inst.ImageBacking,
			Index:            "0",
			Size:             inst.InitDiskSize,
			DeleteProtection: inst.DeleteProtection,
		}

		backingImage, e := data.WriteImage(db, virt.Image, dsk.Id,
			inst.InitDiskSize, inst.ImageBacking)
		if e != nil {
			err = e
			return
		}

		dsk.BackingImage = backingImage

		err = dsk.Insert(db)
		if err != nil {
			return
		}

		_ = event.PublishDispatch(db, "disk.change")

		virt.Disks = append(virt.Disks, &vm.Disk{
			Index: 0,
			Path:  paths.GetDiskPath(dsk.Id),
		})
	}

	err = cloudinit.Write(db, inst, virt, true)
	if err != nil {
		return
	}

	err = writeService(virt)
	if err != nil {
		return
	}

	virt.State = vm.Starting
	err = virt.Commit(db)
	if err != nil {
		return
	}

	err = systemd.Start(unitName)
	if err != nil {
		return
	}

	err = Wait(db, virt)
	if err != nil {
		return
	}

	if virt.Vnc {
		err = qms.VncPassword(virt.Id, inst.VncPassword)
		if err != nil {
			return
		}
	}

	err = NetworkConf(db, virt)
	if err != nil {
		return
	}

	store.RemVirt(virt.Id)
	store.RemDisks(virt.Id)

	return
}

func Destroy(db *database.Database, virt *vm.VirtualMachine) (err error) {
	vmPath := paths.GetVmPath(virt.Id)
	unitName := paths.GetUnitName(virt.Id)
	unitPath := paths.GetUnitPath(virt.Id)
	sockPath := paths.GetSockPath(virt.Id)
	guestPath := paths.GetGuestPath(virt.Id)
	pidPath := paths.GetPidPath(virt.Id)

	logrus.WithFields(logrus.Fields{
		"id": virt.Id.Hex(),
	}).Info("qemu: Destroying virtual machine")

	exists, err := utils.Exists(unitPath)
	if err != nil {
		return
	}

	if exists {
		vrt, e := GetVmInfo(virt.Id, false, true)
		if e != nil {
			err = e
			return
		}

		if vrt.State == vm.Running {
			shutdown := false

			logged := false
			for i := 0; i < 10; i++ {
				err = qms.Shutdown(virt.Id)
				if err == nil {
					break
				}

				if !logged {
					logged = true
					logrus.WithFields(logrus.Fields{
						"instance_id": virt.Id.Hex(),
						"error":       err,
					}).Warn(
						"qemu: Failed to send shutdown to virtual machine")
				}

				time.Sleep(500 * time.Millisecond)
			}

			if err != nil {
				logrus.WithFields(logrus.Fields{
					"id":    virt.Id.Hex(),
					"error": err,
				}).Error("qemu: Power off virtual machine error")
				err = nil
			} else {
				for i := 0; i < settings.Hypervisor.StopTimeout; i++ {
					vrt, err = GetVmInfo(virt.Id, false, true)
					if err != nil {
						return
					}

					if vrt == nil || vrt.State == vm.Stopped ||
						vrt.State == vm.Failed {

						if vrt != nil {
							err = vrt.Commit(db)
							if err != nil {
								return
							}
						}

						shutdown = true
						break
					}

					time.Sleep(1 * time.Second)

					if (i+1)%15 == 0 {
						_ = qms.Shutdown(virt.Id)
					}
				}
			}

			if !shutdown {
				logrus.WithFields(logrus.Fields{
					"id": virt.Id.Hex(),
				}).Warning("qemu: Force power off virtual machine")
			}
		}

		err = systemd.Stop(unitName)
		if err != nil {
			return
		}
	}

	time.Sleep(3 * time.Second)

	err = NetworkConfClear(db, virt)
	if err != nil {
		return
	}

	for i, dsk := range virt.Disks {
		ds, e := disk.Get(db, dsk.GetId())
		if e != nil {
			err = e
			if _, ok := err.(*database.NotFoundError); ok {
				err = nil
				continue
			}
			return
		}

		if i == 0 && ds.SourceInstance == virt.Id {
			err = disk.Delete(db, ds.Id)
			if err != nil {
				if _, ok := err.(*database.NotFoundError); ok {
					err = nil
					continue
				}
				return
			}
		} else {
			err = disk.Detach(db, dsk.GetId())
			if err != nil {
				return
			}
		}
	}

	err = utils.RemoveAll(vmPath)
	if err != nil {
		return
	}

	err = utils.RemoveAll(unitPath)
	if err != nil {
		return
	}

	err = utils.RemoveAll(sockPath)
	if err != nil {
		return
	}

	err = utils.RemoveAll(guestPath)
	if err != nil {
		return
	}

	err = utils.RemoveAll(pidPath)
	if err != nil {
		return
	}

	err = utils.RemoveAll(paths.GetInitPath(virt.Id))
	if err != nil {
		return
	}

	err = utils.RemoveAll(paths.GetLeasePath(virt.Id))
	if err != nil {
		return
	}

	err = utils.RemoveAll(unitPath)
	if err != nil {
		return
	}

	store.RemVirt(virt.Id)
	store.RemDisks(virt.Id)
	store.RemAddress(virt.Id)
	store.RemRoutes(virt.Id)

	return
}

func PowerOn(db *database.Database, inst *instance.Instance,
	virt *vm.VirtualMachine) (err error) {
	unitName := paths.GetUnitName(virt.Id)

	logrus.WithFields(logrus.Fields{
		"id": virt.Id.Hex(),
	}).Info("qemu: Starting virtual machine")

	err = cloudinit.Write(db, inst, virt, false)
	if err != nil {
		return
	}

	err = writeService(virt)
	if err != nil {
		return
	}

	err = systemd.Start(unitName)
	if err != nil {
		return
	}

	err = Wait(db, virt)
	if err != nil {
		return
	}

	if virt.Vnc {
		err = qms.VncPassword(virt.Id, inst.VncPassword)
		if err != nil {
			return
		}
	}

	err = NetworkConf(db, virt)
	if err != nil {
		return
	}

	store.RemVirt(virt.Id)
	store.RemDisks(virt.Id)

	return
}

func Cleanup(db *database.Database, virt *vm.VirtualMachine) {
	logrus.WithFields(logrus.Fields{
		"id": virt.Id.Hex(),
	}).Info("qemu: Stopped virtual machine")

	err := NetworkConfClear(db, virt)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"id":    virt.Id.Hex(),
			"error": err,
		}).Error("qemu: Failed to cleanup virtual machine network")
	}

	time.Sleep(3 * time.Second)

	store.RemVirt(virt.Id)
	store.RemDisks(virt.Id)

	return
}

func PowerOff(db *database.Database, virt *vm.VirtualMachine) (err error) {
	unitName := paths.GetUnitName(virt.Id)

	logrus.WithFields(logrus.Fields{
		"id": virt.Id.Hex(),
	}).Info("qemu: Stopping virtual machine")

	logged := false
	for i := 0; i < 10; i++ {
		err = qms.Shutdown(virt.Id)
		if err == nil {
			break
		}

		if !logged {
			logged = true
			logrus.WithFields(logrus.Fields{
				"instance_id": virt.Id.Hex(),
				"error":       err,
			}).Warn("qemu: Failed to send shutdown to virtual machine")
		}

		time.Sleep(500 * time.Millisecond)
	}

	shutdown := false
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"id":    virt.Id.Hex(),
			"error": err,
		}).Error("qemu: Power off virtual machine error")
		err = nil
	} else {
		for i := 0; i < settings.Hypervisor.StopTimeout; i++ {
			vrt, e := GetVmInfo(virt.Id, false, true)
			if e != nil {
				err = e
				return
			}

			if vrt == nil || vrt.State == vm.Stopped ||
				vrt.State == vm.Failed {

				if vrt != nil {
					err = vrt.Commit(db)
					if err != nil {
						return
					}
				}

				shutdown = true
				break
			}

			time.Sleep(1 * time.Second)

			if (i+1)%15 == 0 {
				_ = qms.Shutdown(virt.Id)
			}
		}
	}

	if !shutdown {
		logrus.WithFields(logrus.Fields{
			"id": virt.Id.Hex(),
		}).Warning("qemu: Force power off virtual machine")

		err = systemd.Stop(unitName)
		if err != nil {
			return
		}
	}

	err = NetworkConfClear(db, virt)
	if err != nil {
		return
	}

	time.Sleep(3 * time.Second)

	store.RemVirt(virt.Id)
	store.RemDisks(virt.Id)

	return
}
