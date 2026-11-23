// SPDX-License-Identifier: GPL-3.0-or-later

// Command aether-winres generates Aether Time's Windows resources:
//
//	-syso DIR    rsrc_windows_{amd64,arm64}.syso (icon, version info,
//	             manifest) for the Go linker
//	-assets DIR  aether-time.ico and sidebar.bmp for the installer
//
// The release build runs it with -version so Properties shows the real
// version; the committed .syso files carry a development version.
package main

import (
	"flag"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/KK4ODA/Aether-Time/internal/icon"
	"github.com/KK4ODA/Aether-Time/internal/version"
	"github.com/KK4ODA/Aether-Time/internal/winres"
)

func main() {
	ver := flag.String("version", "0.0.0-dev", "version to record in the program file")
	sysoDir := flag.String("syso", "", "write .syso files into this directory")
	assetsDir := flag.String("assets", "", "write installer icon and bitmap into this directory")
	flag.Parse()
	if *sysoDir == "" && *assetsDir == "" {
		fmt.Fprintln(os.Stderr, "usage: aether-winres [-version V] [-syso DIR] [-assets DIR]")
		os.Exit(2)
	}
	if err := run(*ver, *sysoDir, *assetsDir); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// AppIcons are the sizes Windows uses for program files, shortcuts and the taskbar.
func appIcons() []*image.NRGBA {
	var imgs []*image.NRGBA
	for _, s := range []int{256, 64, 48, 40, 32, 24, 20, 16} {
		imgs = append(imgs, icon.App(s))
	}
	return imgs
}

// numeric turns "1.2.3-beta.1" into {1, 2, 3, 0}.
func numeric(v string) [4]uint16 {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out [4]uint16
	for i, p := range strings.SplitN(v, ".", 4) {
		n, _ := strconv.Atoi(p)
		out[i] = uint16(n)
	}
	return out
}

const manifestTmpl = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <assemblyIdentity type="win32" name="AetherTime" version="%d.%d.%d.%d" processorArchitecture="*"/>
  <description>Aether Time</description>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security>
      <requestedPrivileges>
        <requestedExecutionLevel level="asInvoker" uiAccess="false"/>
      </requestedPrivileges>
    </security>
  </trustInfo>
  <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1">
    <application>
      <supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/>
      <supportedOS Id="{1f676c76-80e1-4239-95bb-83d0f6d0da78}"/>
      <supportedOS Id="{4a2f28e3-53b9-4441-ba9c-d69d4a4a6e38}"/>
      <supportedOS Id="{35138b9a-5d96-4fbd-8e2d-a2440225f93a}"/>
    </application>
  </compatibility>
  <application xmlns="urn:schemas-microsoft-com:asm.v3">
    <windowsSettings>
      <dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true/pm</dpiAware>
      <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">PerMonitorV2, PerMonitor</dpiAwareness>
      <longPathAware xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">true</longPathAware>
    </windowsSettings>
  </application>
  <dependency>
    <dependentAssembly>
      <assemblyIdentity type="win32" name="Microsoft.Windows.Common-Controls" version="6.0.0.0" processorArchitecture="*" publicKeyToken="6595b64144ccf1df" language="*"/>
    </dependentAssembly>
  </dependency>
</assembly>
`

// Resources returns everything embedded into aether-time.exe.
func Resources(ver string) winres.Resources {
	n := numeric(ver)
	ver = strings.TrimPrefix(ver, "v")
	return winres.Resources{
		Icons: appIcons(),
		Version: winres.Version{
			File:    n,
			Product: n,
			Strings: map[string]string{
				"CompanyName":      "KK4ODA",
				"FileDescription":  version.AppName,
				"FileVersion":      ver,
				"InternalName":     version.BinaryName,
				"LegalCopyright":   "Copyright (C) 2026 KK4ODA and contributors. GPL-3.0-or-later.",
				"OriginalFilename": version.BinaryName + ".exe",
				"ProductName":      version.AppName,
				"ProductVersion":   ver,
				"Comments":         "Keeps the clock exact for FT8 and other digital modes.",
			},
		},
		Manifest: fmt.Sprintf(manifestTmpl, n[0], n[1], n[2], n[3]),
	}
}

func run(ver, sysoDir, assetsDir string) error {
	if sysoDir != "" {
		res := Resources(ver)
		for _, arch := range []string{"amd64", "arm64"} {
			obj, err := winres.Object(arch, res)
			if err != nil {
				return err
			}
			p := filepath.Join(sysoDir, "rsrc_windows_"+arch+".syso")
			if err := os.WriteFile(p, obj, 0o644); err != nil {
				return err
			}
			fmt.Println("wrote", p)
		}
	}
	if assetsDir != "" {
		if err := os.MkdirAll(assetsDir, 0o755); err != nil {
			return err
		}
		files := map[string][]byte{
			"aether-time.ico": icon.ICO(appIcons()...),
			"sidebar.bmp":     icon.BMP(icon.InstallerSidebar(), icon.Dial),
		}
		for name, data := range files {
			p := filepath.Join(assetsDir, name)
			if err := os.WriteFile(p, data, 0o644); err != nil {
				return err
			}
			fmt.Println("wrote", p)
		}
	}
	return nil
}
