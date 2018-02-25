package qemu

import (
	"fmt"
	"github.com/pritunl/pritunl-cloud/paths"
	"gopkg.in/mgo.v2/bson"
	"strings"
)

type Disk struct {
	Media   string
	Index   int
	File    string
	Format  string
	Discard bool
}

type Network struct {
	Type       string
	Iface      string
	MacAddress string
	Bridge     string
}

type Qemu struct {
	Id       bson.ObjectId
	Data     string
	Kvm      bool
	Machine  string
	Accel    string
	Cpu      string
	Cpus     int
	Cores    int
	Threads  int
	Boot     string
	Memory   int
	Disks    []*Disk
	Networks []*Network
}

func (q *Qemu) Marshal() (output string, err error) {
	cmd := []string{
		"/usr/bin/qemu-system-x86_64",
		"-nographic",
	}

	if q.Kvm {
		cmd = append(cmd, "-enable-kvm")
	}

	cmd = append(cmd, "-name")
	cmd = append(cmd, fmt.Sprintf("pritunl_%s", q.Id.Hex()))

	cmd = append(cmd, "-machine")
	accel := ""
	if q.Accel != "" {
		accel = ",accel=kvm"
	}
	cmd = append(cmd, fmt.Sprintf("type=%s%s", q.Machine, accel))

	cmd = append(cmd, "-cpu")
	cmd = append(cmd, q.Cpu)

	cmd = append(cmd, "-smp")
	cmd = append(cmd, fmt.Sprintf(
		"cpus=%d,cores=%d,threads=%d",
		q.Cpus,
		q.Cores,
		q.Threads,
	))

	cmd = append(cmd, "-boot")
	cmd = append(cmd, q.Boot)

	cmd = append(cmd, "-m")
	cmd = append(cmd, fmt.Sprintf("%dM", q.Memory))

	for _, disk := range q.Disks {
		additional := ""
		if disk.Discard {
			additional += ",discard=on"
		}
		if disk.Media == "disk" {
			additional += ",if=virtio"
		}

		cmd = append(cmd, "-drive")
		cmd = append(cmd, fmt.Sprintf(
			"file=%s,index=%d,media=%s,format=%s%s",
			disk.File,
			disk.Index,
			disk.Media,
			disk.Format,
			additional,
		))
	}

	for _, network := range q.Networks {
		cmd = append(cmd, "-net")
		net := network.Type

		if network.MacAddress != "" {
			net += fmt.Sprintf(",macaddr=%s", network.MacAddress)
		}

		if network.Bridge != "" {
			net = fmt.Sprintf(
				"tap,vlan=0,ifname=%s,script=no",
				network.Iface,
			)
		}

		cmd = append(cmd, net)
	}

	cmd = append(cmd, "-monitor")
	cmd = append(cmd, fmt.Sprintf(
		"unix:%s,server,nowait",
		paths.GetSockPath(q.Id),
	))

	cmd = append(cmd, "-pidfile")
	cmd = append(cmd, paths.GetPidPath(q.Id))

	guestPath := paths.GetGuestPath(q.Id)
	cmd = append(cmd, "-chardev")
	cmd = append(cmd, fmt.Sprintf(
		"socket,path=%s,server,nowait,id=guest", guestPath))
	cmd = append(cmd, "-device")
	cmd = append(cmd, "virtio-serial")
	cmd = append(cmd, "-device")
	cmd = append(cmd,
		"virtserialport,chardev=guest,name=org.qemu.guest_agent.0")

	//cmd = append(cmd, "-vnc")
	//cmd = append(cmd, ":1")

	output = fmt.Sprintf(
		systemdTemplate,
		q.Data,
		strings.Join(cmd, " "),
	)
	return
}
