/// <reference path="../References.d.ts"/>
export const SYNC = 'instance.sync';
export const SYNC_NODE = 'instance.sync_node';
export const TRAVERSE = 'instance.traverse';
export const FILTER = 'instance.filter';
export const CHANGE = 'instance.change';

export interface Instance {
	id?: string;
	organization?: string;
	zone?: string;
	node?: string;
	image?: string;
	image_backing?: boolean;
	status?: string;
	uptime?: string;
	state?: string;
	vm_state?: string;
	vm_timestamp?: string;
	delete_protection?: boolean;
	public_ips?: string[];
	public_ips6?: string[];
	private_ips?: string[];
	private_ips6?: string[];
	host_ips?: string[];
	public_mac?: string;
	name?: string;
	comment?: string;
	init_disk_size?: number;
	memory?: number;
	processors?: number;
	network_roles?: string[];
	usb_devices?: UsbDevice[];
	vnc?: boolean;
	vnc_password?: string;
	vnc_display?: number;
	domain?: string;
	no_public_address?: boolean;
	no_host_address?: boolean;
	vpc?: string;
	subnet?: string;
	count?: number;
	info?: Info;
}

export interface Filter {
	id?: string;
	name?: string;
	state?: string;
	network_role?: string;
	organization?: string;
	node?: string;
	zone?: string;
	vpc?: string;
}

export interface UsbDevice {
	name?: string;
	vendor?: string;
	product?: string;
}

export interface Info {
	node?: string;
	firewall_rules?: string[];
	authorities?: string[];
	disks?: string[];
	usb_devices?: UsbDevice[];
}

export type Instances = Instance[];
export type InstancesNode = Map<string, Instances>;

export type InstanceRo = Readonly<Instance>;
export type InstancesRo = ReadonlyArray<InstanceRo>;
export type InstancesNodeRo = Map<string, InstancesRo>;

export interface InstanceDispatch {
	type: string;
	data?: {
		id?: string;
		node?: string;
		instance?: Instance;
		instances?: Instances;
		page?: number;
		pageCount?: number;
		filter?: Filter;
		count?: number;
	};
}
