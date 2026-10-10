package part

import (
	"strconv"
	"strings"
)

// TypeName returns the readable name of a partition type, "" when it is
// not known. scheme is that of Partition.Scheme; typeID is as in
// Partition.TypeID: "0x83" for MBR and EBR, a GUID for GPT, the type text
// for APM, a number for BSD and VTOC.
func TypeName(scheme, typeID string) string {
	switch scheme {
	case "MBR", "EBR":
		if v, err := strconv.ParseUint(typeID, 0, 8); err == nil {
			return mbrTypes[byte(v)]
		}
	case "GPT":
		return gptTypes[strings.ToUpper(strings.Trim(typeID, "{}"))]
	case "APM":
		if n, ok := apmTypes[typeID]; ok {
			return n
		}
		if strings.HasPrefix(typeID, "Apple_Driver") {
			return "Apple driver"
		}
	case "BSD":
		if v, err := strconv.ParseUint(typeID, 0, 8); err == nil && int(v) < len(bsdTypes) {
			return bsdTypes[v]
		}
	case "VTOC":
		if v, err := strconv.ParseUint(typeID, 0, 16); err == nil {
			return vtocTags[uint16(v)]
		}
	}
	return ""
}

// The MBR type bytes, the names close to those of fdisk
var mbrTypes = map[byte]string{
	0x00: "Empty",
	0x01: "FAT12",
	0x02: "XENIX root",
	0x03: "XENIX usr",
	0x04: "FAT16 <32M",
	0x05: "Extended",
	0x06: "FAT16",
	0x07: "NTFS/exFAT",
	0x08: "AIX",
	0x09: "AIX bootable",
	0x0A: "OS/2 Boot Manager",
	0x0B: "FAT32",
	0x0C: "FAT32 (LBA)",
	0x0E: "FAT16 (LBA)",
	0x0F: "Extended (LBA)",
	0x10: "OPUS",
	0x11: "Hidden FAT12",
	0x12: "Compaq diagnostics",
	0x14: "Hidden FAT16 <32M",
	0x16: "Hidden FAT16",
	0x17: "Hidden NTFS/exFAT",
	0x18: "AST SmartSleep",
	0x1B: "Hidden FAT32",
	0x1C: "Hidden FAT32 (LBA)",
	0x1E: "Hidden FAT16 (LBA)",
	0x24: "NEC DOS",
	0x27: "Hidden NTFS WinRE",
	0x39: "Plan 9",
	0x3C: "PartitionMagic recovery",
	0x40: "Venix 80286",
	0x41: "PPC PReP Boot",
	0x42: "Windows dynamic disk",
	0x4D: "QNX4.x",
	0x4E: "QNX4.x 2nd part",
	0x4F: "QNX4.x 3rd part",
	0x50: "OnTrack DM",
	0x51: "OnTrack DM6 Aux1",
	0x52: "CP/M",
	0x53: "OnTrack DM6 Aux3",
	0x54: "OnTrack DM6",
	0x55: "EZ-Drive",
	0x56: "Golden Bow",
	0x5C: "Priam Edisk",
	0x61: "SpeedStor",
	0x63: "GNU HURD or SysV",
	0x64: "Novell Netware 286",
	0x65: "Novell Netware 386",
	0x70: "DiskSecure Multi-Boot",
	0x75: "PC/IX",
	0x80: "Old Minix",
	0x81: "Minix / old Linux",
	0x82: "Linux swap / Solaris",
	0x83: "Linux",
	0x84: "OS/2 hidden C: / Intel hibernation",
	0x85: "Linux extended",
	0x86: "NTFS volume set",
	0x87: "NTFS volume set",
	0x88: "Linux plaintext",
	0x8E: "Linux LVM",
	0x93: "Amoeba",
	0x94: "Amoeba BBT",
	0x9F: "BSD/OS",
	0xA0: "IBM Thinkpad hibernation",
	0xA5: "FreeBSD",
	0xA6: "OpenBSD",
	0xA7: "NeXTSTEP",
	0xA8: "Darwin UFS",
	0xA9: "NetBSD",
	0xAB: "Darwin boot",
	0xAF: "HFS / HFS+",
	0xB7: "BSDI fs",
	0xB8: "BSDI swap",
	0xBB: "Boot Wizard hidden",
	0xBC: "Acronis FAT32 LBA",
	0xBE: "Solaris boot",
	0xBF: "Solaris",
	0xC1: "DRDOS/sec (FAT12)",
	0xC4: "DRDOS/sec (FAT16 <32M)",
	0xC6: "DRDOS/sec (FAT16)",
	0xC7: "Syrinx",
	0xDA: "Non-FS data",
	0xDB: "CP/M / CTOS",
	0xDE: "Dell Utility",
	0xDF: "BootIt",
	0xE1: "DOS access",
	0xE3: "DOS R/O",
	0xE4: "SpeedStor",
	0xEA: "Linux extended boot",
	0xEB: "BeOS fs",
	0xEE: "GPT protective",
	0xEF: "EFI (FAT12/16/32)",
	0xF0: "Linux/PA-RISC boot",
	0xF1: "SpeedStor",
	0xF2: "DOS secondary",
	0xF4: "SpeedStor",
	0xF8: "EBBR protective",
	0xFB: "VMware VMFS",
	0xFC: "VMware VMKCORE",
	0xFD: "Linux raid autodetect",
	0xFE: "LANstep",
	0xFF: "BBT",
}

// The GPT partition type GUIDs, as Windows writes them
var gptTypes = map[string]string{
	"00000000-0000-0000-0000-000000000000": "Unused",
	"C12A7328-F81F-11D2-BA4B-00A0C93EC93B": "EFI System",
	"024DEE41-33E7-11D3-9D69-0008C781F39F": "MBR partition scheme",
	"21686148-6449-6E6F-744E-656564454649": "BIOS boot",
	"D3BFE2DE-3DAF-11DF-BA40-E3A556D89593": "Intel Fast Flash",
	"F4019732-066E-4E12-8273-346C5641494F": "Sony boot partition",
	"BFBFAFE7-A34F-448A-9A5B-6213EB736C22": "Lenovo boot partition",
	"9E1A2D38-C612-4316-AA26-8B49521E5A8B": "PowerPC PReP boot",

	// Windows
	"E3C9E316-0B5C-4DB8-817D-F92DF00215AE": "Microsoft reserved",
	"EBD0A0A2-B9E5-4433-87C0-68B6B72699C7": "Microsoft basic data",
	"5808C8AA-7E8F-42E0-85D2-E1E90434CFB3": "Microsoft LDM metadata",
	"AF9B60A0-1431-4F62-BC68-3311714A69AD": "Microsoft LDM data",
	"DE94BBA4-06D1-4D40-A16A-BFD50179D6AC": "Windows recovery environment",
	"37AFFC90-EF7D-4E96-91C3-2D7AE055B174": "IBM General Parallel Fs",
	"E75CAF8F-F680-4CEE-AFA3-B001E56EFC2D": "Microsoft Storage Spaces",
	"558D43C5-A1AC-43C0-AAC8-D1472B2923D1": "Microsoft Storage Replica",

	// HP-UX
	"75894C1E-3AEB-11D3-B7C1-7B03A0000000": "HP-UX data",
	"E2A1E728-32E3-11D6-A682-7B03A0000000": "HP-UX service",

	// Linux
	"0FC63DAF-8483-4772-8E79-3D69D8477DE4": "Linux filesystem",
	"A19D880F-05FC-4D3B-A006-743F0F84911E": "Linux RAID",
	"0657FD6D-A4AB-43C4-84E5-0933C84B4F4F": "Linux swap",
	"E6D6D379-F507-44C2-A23C-238F2A3DF928": "Linux LVM",
	"933AC7E1-2EB4-4F13-B844-0E14E2AEF915": "Linux home",
	"3B8F8425-20E0-4F3B-907F-1A25A76F98E8": "Linux server data",
	"773F91EF-66D4-49B5-BD83-D683BF40AD16": "Linux user's home",
	"BC13C2FF-59E6-4262-A352-B275FD6F7172": "Linux extended boot",
	"4D21B016-B534-45C2-A9FB-5C16E091FD2D": "Linux variable data",
	"7EC6F557-3BC5-4ACA-B293-16EF5DF639D1": "Linux temporary data",
	"CA7D7CCB-63ED-4C53-861C-1742536059CC": "Linux LUKS",
	"7FFEC5C9-2D00-49B7-8941-3EA10A5586B7": "Linux dm-crypt",
	"8DA63339-0007-60C0-C436-083AC8230908": "Linux reserved",
	"44479540-F297-41B2-9AF7-D131D5F0458A": "Linux root (x86)",
	"4F68BCE3-E8CD-4DB1-96E7-FBCAF984B709": "Linux root (x86-64)",
	"69DAD710-2CE4-4E3C-B16C-21A1D49ABED3": "Linux root (ARM)",
	"B921B045-1DF0-41C3-AF44-4C6F280D3FAE": "Linux root (ARM-64)",
	"993D8D3D-F80E-4225-855A-9DAF8ED7EA97": "Linux root (IA-64)",
	"60D5A7FE-8E7D-435C-B714-3DD8162144E1": "Linux root (RISC-V-32)",
	"72EC70A6-CF74-40E6-BD49-4BDA08E8F224": "Linux root (RISC-V-64)",
	"8484680C-9521-48C6-9C11-B0720656F69E": "Linux /usr (x86-64)",
	"B0E01050-EE5F-4390-949A-9101B17104E9": "Linux /usr (ARM-64)",

	// FreeBSD, NetBSD, OpenBSD
	"83BD6B9D-7F41-11DC-BE0B-001560B84F0F": "FreeBSD boot",
	"516E7CB4-6ECF-11D6-8FF8-00022D09712B": "FreeBSD disklabel",
	"516E7CB5-6ECF-11D6-8FF8-00022D09712B": "FreeBSD swap",
	"516E7CB6-6ECF-11D6-8FF8-00022D09712B": "FreeBSD UFS",
	"516E7CB8-6ECF-11D6-8FF8-00022D09712B": "FreeBSD Vinum",
	"516E7CBA-6ECF-11D6-8FF8-00022D09712B": "FreeBSD ZFS",
	"74BA7DD9-A689-11E1-BD04-00E081286ACF": "FreeBSD nandfs",
	"49F48D32-B10E-11DC-B99B-0019D1879648": "NetBSD swap",
	"49F48D5A-B10E-11DC-B99B-0019D1879648": "NetBSD FFS",
	"49F48D82-B10E-11DC-B99B-0019D1879648": "NetBSD LFS",
	"49F48DAA-B10E-11DC-B99B-0019D1879648": "NetBSD RAID",
	"2DB519C4-B10F-11DC-B99B-0019D1879648": "NetBSD concatenated",
	"2DB519EC-B10F-11DC-B99B-0019D1879648": "NetBSD encrypted",
	"824CC7A0-36A8-11E3-890A-952519AD3F61": "OpenBSD data",

	// Apple
	"48465300-0000-11AA-AA11-00306543ECAC": "Apple HFS/HFS+",
	"7C3457EF-0000-11AA-AA11-00306543ECAC": "Apple APFS",
	"55465300-0000-11AA-AA11-00306543ECAC": "Apple UFS",
	"52414944-0000-11AA-AA11-00306543ECAC": "Apple RAID",
	"52414944-5F4F-11AA-AA11-00306543ECAC": "Apple RAID offline",
	"426F6F74-0000-11AA-AA11-00306543ECAC": "Apple boot",
	"4C616265-6C00-11AA-AA11-00306543ECAC": "Apple label",
	"5265636F-7665-11AA-AA11-00306543ECAC": "Apple TV recovery",
	"53746F72-6167-11AA-AA11-00306543ECAC": "Apple Core storage",
	"69646961-6700-11AA-AA11-00306543ECAC": "Apple Silicon boot",
	"52637672-7900-11AA-AA11-00306543ECAC": "Apple Silicon recovery",

	// Solaris (and the ZFS of Apple)
	"6A82CB45-1DD2-11B2-99A6-080020736631": "Solaris boot",
	"6A85CF4D-1DD2-11B2-99A6-080020736631": "Solaris root",
	"6A87C46F-1DD2-11B2-99A6-080020736631": "Solaris swap",
	"6A8B642B-1DD2-11B2-99A6-080020736631": "Solaris backup",
	"6A898CC3-1DD2-11B2-99A6-080020736631": "Solaris /usr & Apple ZFS",
	"6A8EF2E9-1DD2-11B2-99A6-080020736631": "Solaris /var",
	"6A90BA39-1DD2-11B2-99A6-080020736631": "Solaris /home",

	// ChromeOS
	"FE3A2A5D-4F32-41A7-B725-ACCC3285A309": "ChromeOS kernel",
	"3CB8E202-3B7E-47DD-8A3C-7FF2A13CFCEC": "ChromeOS root fs",
	"2E0A753D-9E48-43B0-8337-B15192CB1B5E": "ChromeOS reserved",
	"CAB6E88E-ABF3-4102-A07A-D4BB9BE3C1D3": "ChromeOS firmware",
	"09845860-705F-4BB5-B16C-8A8A099CAF52": "ChromeOS miniOS",
	"3F0F8318-F146-4E6B-8222-C28C8F02E0D5": "ChromeOS hibernate",

	// Android
	"2568845D-2332-4675-BC39-8FA5A4748D15": "Android bootloader",
	"114EAFFE-1552-4022-B26E-9B053604CF84": "Android bootloader 2",
	"49A4D17F-93A3-45C1-A0DE-F50B2EBE2599": "Android boot",
	"4177C722-9E92-4AAB-8644-43502BFD5506": "Android recovery",
	"EF32A33B-A409-486C-9141-9FFB711F6266": "Android misc",
	"20AC26BE-20B7-11E3-84C5-6CFDB94711E9": "Android metadata",
	"38F428E6-D326-425D-9140-6E0EA133647C": "Android system",
	"A893EF21-E428-470A-9E55-0668FD91A2D9": "Android cache",
	"DC76DDA9-5AC1-491C-AF42-A82591580C0D": "Android data",
	"EBC597D0-2053-4B15-8B64-E0AAC75F4DB1": "Android persistent",
	"8F68CC74-C5E5-48DA-BE91-A0C8C15E9C80": "Android factory",
	"767941D0-2085-11E3-AD3B-6CFDB94711E9": "Android fastboot/tertiary",
	"AC6D7924-EB71-4DF8-B48D-E267B27148FF": "Android OEM",

	// Others
	"42465331-3BA3-10F1-802A-4861696B7521": "Haiku BFS",
	"AA31E02A-400F-11DB-9590-000C2911D1B8": "VMware VMFS",
	"9198EFFC-31C0-11DB-8F78-000C2911D1B8": "VMware reserved",
	"9D275380-40AD-11DB-BF97-000C2911D1B8": "VMware kcore crash protection",
}

// The partition types of the Apple Partition Map
var apmTypes = map[string]string{
	"Apple_partition_map": "Partition map",
	"Apple_Driver":        "Apple driver",
	"Apple_Driver43":      "Apple SCSI driver",
	"Apple_Driver_ATA":    "Apple ATA driver",
	"Apple_Driver_ATAPI":  "Apple ATAPI driver",
	"Apple_FWDriver":      "Apple FireWire driver",
	"Apple_Patches":       "Apple patches",
	"Apple_HFS":           "HFS/HFS+",
	"Apple_HFSX":          "HFSX",
	"Apple_APFS":          "APFS",
	"Apple_UFS":           "UFS",
	"Apple_MFS":           "MFS",
	"Apple_PRODOS":        "ProDOS",
	"Apple_Boot":          "Apple boot",
	"Apple_Bootstrap":     "Apple bootstrap",
	"Apple_Boot_RAID":     "Apple RAID boot",
	"Apple_RAID":          "Apple RAID",
	"Apple_Free":          "Free space",
	"Apple_Scratch":       "Empty",
	"Apple_Void":          "Void",
	"Apple_Unix_SVR2":     "Unix file system",
	"Linux":               "Linux",
	"Linux_swap":          "Linux swap",
	"DOS_FAT_12":          "FAT12",
	"DOS_FAT_16":          "FAT16",
	"DOS_FAT_32":          "FAT32",
	"Windows_FAT_32":      "FAT32",
	"Windows_NTFS":        "NTFS",
}

// The file system types of the BSD disklabel (p_fstype), as NetBSD has them
var bsdTypes = []string{
	"Unused", "Swap", "Version 6", "Version 7", "System V", "4.1BSD",
	"Eighth Edition", "4.2BSD (FFS)", "MS-DOS", "4.4LFS", "Unknown", "HPFS",
	"ISO9660", "Boot", "ADOS", "HFS", "FILECORE", "ext2fs", "NTFS", "RAID",
	"ccd", "jfs2", "Apple UFS", "vinum", "UDF", "SysV BFS", "EFS", "ZFS",
	"nilfs", "cgd", "MINIX FSv3", "Apple UFS", "LVM",
}

// The tags of the Sun VTOC slices (v_part[].p_tag)
var vtocTags = map[uint16]string{
	0x00: "Unassigned",
	0x01: "Boot",
	0x02: "Root",
	0x03: "Swap",
	0x04: "Usr",
	0x05: "Whole disk",
	0x06: "Stand",
	0x07: "Var",
	0x08: "Home",
	0x09: "Alternate sectors",
	0x0A: "Cache",
	0x0B: "Reserved",
	0x0C: "EFI System",
	0x82: "Linux swap",
	0x83: "Linux native",
	0x8E: "Linux LVM",
	0xFD: "Linux raid autodetect",
}
