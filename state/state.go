package state

import (
	"github.com/Sirupsen/logrus"
	"github.com/dropbox/godropbox/container/set"
	"github.com/pritunl/pritunl-cloud/database"
	"github.com/pritunl/pritunl-cloud/disk"
	"github.com/pritunl/pritunl-cloud/instance"
	"github.com/pritunl/pritunl-cloud/node"
	"github.com/pritunl/pritunl-cloud/qemu"
	"github.com/pritunl/pritunl-cloud/vm"
	"gopkg.in/mgo.v2/bson"
)

type State struct {
	disks []*disk.Disk

	virtsMap     map[bson.ObjectId]*vm.VirtualMachine
	instances    []*instance.Instance
	instancesMap map[bson.ObjectId]*instance.Instance
	addInstances set.Set
	remInstances set.Set
}

func (s *State) Instances() []*instance.Instance {
	return s.instances
}

func (s *State) Disks() []*disk.Disk {
	return s.disks
}

func (s *State) DiskInUse(instId, dskId bson.ObjectId) bool {
	curVirt := s.virtsMap[instId]

	if curVirt != nil {
		for _, vmDsk := range curVirt.Disks {
			if vmDsk.GetId() == dskId {
				return true
			}
		}
	}

	return false
}

//func (s *State) InstanceExists(instId bson.ObjectId) bool {
//	_, ok := s.virtsMap[instId]
//	return ok
//}

func (s *State) GetVirt(instId bson.ObjectId) *vm.VirtualMachine {
	return s.virtsMap[instId]
}

func (s *State) init() (err error) {
	db := database.GetDatabase()
	defer db.Close()

	disks, err := disk.GetNode(db, node.Self.Id)
	if err != nil {
		return
	}
	s.disks = disks

	curVirts, err := qemu.GetVms(db)
	if err != nil {
		return
	}

	virtsId := set.NewSet()
	virtsMap := map[bson.ObjectId]*vm.VirtualMachine{}
	for _, virt := range curVirts {
		virtsId.Add(virt.Id)
		virtsMap[virt.Id] = virt
	}
	s.virtsMap = virtsMap

	instances, err := instance.GetAllVirt(db, &bson.M{
		"node": node.Self.Id,
	}, disks)
	s.instances = instances

	for _, inst := range instances {
		virtsId.Remove(inst.Id)
	}

	for virtId := range virtsId.Iter() {
		logrus.WithFields(logrus.Fields{
			"id": virtId.(bson.ObjectId).Hex(),
		}).Info("sync: Unknown instance")
	}

	return
}

func GetState() (stat *State, err error) {
	stat = &State{}

	err = stat.init()
	if err != nil {
		return
	}

	return
}