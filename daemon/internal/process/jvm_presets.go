package process

import "fmt"

// JvmPreset is a named set of JVM flags a server can opt into. Memory sizing
// stays with the server's own min/max settings; presets only add tuning flags.
type JvmPreset struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Flags is what the preset adds for a server with the given heap size. It is
	// filled in by JvmPresets so the dashboard can show the exact arguments.
	Flags []string `json:"flags"`
}

// JvmPresetAikar is Aikar's G1GC flags, the usual recommendation for Paper
// and its forks (https://docs.papermc.io/paper/aikars-flags).
const JvmPresetAikar = "aikar"

// JvmPresets lists the presets with their flags worked out for maxMemoryMB.
func JvmPresets(maxMemoryMB int) []JvmPreset {
	return []JvmPreset{
		{
			ID:          "",
			Name:        "Default",
			Description: "No extra tuning. Java picks its own garbage collector settings. Fine for small, lightly loaded servers.",
			Flags:       []string{},
		},
		{
			ID:          JvmPresetAikar,
			Name:        "Aikar's flags",
			Description: "G1 garbage collector tuned for Minecraft. Shorter lag spikes on busy Paper, Purpur and Folia servers; also fine for Fabric and Forge. Works best when the minimum and maximum memory are equal.",
			Flags:       JvmPresetFlags(JvmPresetAikar, maxMemoryMB),
		},
	}
}

// ValidJvmPreset reports whether id names a known preset ("" is the default).
func ValidJvmPreset(id string) bool {
	return id == "" || id == JvmPresetAikar
}

// JvmPresetFlags returns the JVM arguments for a preset, or nil for the
// default/unknown preset.
func JvmPresetFlags(id string, maxMemoryMB int) []string {
	if id != JvmPresetAikar {
		return nil
	}
	// Aikar's guide uses larger young generations and regions above 12 GB.
	large := maxMemoryMB > 12*1024
	newSize, maxNewSize, regionSize, reserve, ihop := "30", "40", "8M", "20", "15"
	if large {
		newSize, maxNewSize, regionSize, reserve, ihop = "40", "50", "16M", "15", "20"
	}
	return []string{
		"-XX:+UseG1GC",
		"-XX:+ParallelRefProcEnabled",
		"-XX:MaxGCPauseMillis=200",
		"-XX:+UnlockExperimentalVMOptions",
		"-XX:+DisableExplicitGC",
		"-XX:+AlwaysPreTouch",
		fmt.Sprintf("-XX:G1NewSizePercent=%s", newSize),
		fmt.Sprintf("-XX:G1MaxNewSizePercent=%s", maxNewSize),
		fmt.Sprintf("-XX:G1HeapRegionSize=%s", regionSize),
		fmt.Sprintf("-XX:G1ReservePercent=%s", reserve),
		"-XX:G1HeapWastePercent=5",
		"-XX:G1MixedGCCountTarget=4",
		fmt.Sprintf("-XX:InitiatingHeapOccupancyPercent=%s", ihop),
		"-XX:G1MixedGCLiveThresholdPercent=90",
		"-XX:G1RSetUpdatingPauseTimePercent=5",
		"-XX:SurvivorRatio=32",
		"-XX:+PerfDisableSharedMem",
		"-XX:MaxTenuringThreshold=1",
		"-Dusing.aikars.flags=https://mcflags.emc.gs",
		"-Daikars.new.flags=true",
	}
}
