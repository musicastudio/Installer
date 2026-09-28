package main

import (
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// hide keeps console tools (schtasks, powershell) from flashing a console window.
func hide(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
}

const uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\`

// addUninstallEntry lists the product in Settings > Apps, uninstalling through the kept setup.
func addUninstallEntry(exe string, size int64) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, uninstallKey+man.Product.ID, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	for name, v := range map[string]string{
		"DisplayName":          man.Product.Name,
		"DisplayVersion":       man.Product.Version,
		"Publisher":            man.Product.Publisher,
		"DisplayIcon":          exe,
		"InstallLocation":      filepath.Dir(exe),
		"UninstallString":      `"` + exe + `" -uninstall`,
		"QuietUninstallString": `"` + exe + `" -uninstall -silent`,
	} {
		if err := k.SetStringValue(name, v); err != nil {
			return err
		}
	}
	for name, v := range map[string]uint32{"NoModify": 1, "NoRepair": 1, "EstimatedSize": uint32(size >> 10)} {
		if err := k.SetDWordValue(name, v); err != nil {
			return err
		}
	}
	return nil
}

func removeUninstallEntry() {
	registry.DeleteKey(registry.LOCAL_MACHINE, uninstallKey+man.Product.ID)
}

func alert(title, msg string) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(msg)
	windows.MessageBox(0, m, t, windows.MB_OK|windows.MB_ICONERROR)
}

// deleteOnReboot has Windows delete p at the next restart (needs admin, which the setup has).
func deleteOnReboot(p string) {
	if u, err := windows.UTF16PtrFromString(p); err == nil {
		windows.MoveFileEx(u, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
	}
}
