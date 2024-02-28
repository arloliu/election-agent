package agent

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"

	"election-agent/internal/logging"

	"github.com/KimMachineGun/automemlimit/memlimit"
	"github.com/pbnjay/memory"
	"go.uber.org/automaxprocs/maxprocs"
)

func AutoSetProcsMem() {
	// Automatically set GOMAXPROCS based on the number of processors
	_, err := maxprocs.Set()
	if err != nil {
		logging.Warnw("Failed to set GOMAXPROCS", "error", err)
	}

	logging.Infow("CPU information", "GOMAXPROCS", runtime.GOMAXPROCS(0), "num_cpu", runtime.NumCPU())

	// Automatically set GOMEMLIMIT based on cgroup memory setting or system total memory
	_, ok := os.LookupEnv("GOMEMLIMIT")
	if ok {
		mem := debug.SetMemoryLimit(-1)
		logging.Infow("Set memory limit by GOMEMLIMIT environment variable", "mem", memByteToStr(mem))
	} else {
		sysTotalMem := memory.TotalMemory()
		limit, err := memlimit.FromCgroup()
		if err == nil && limit < sysTotalMem {
			// set memory limitation to 90% of cgroup allocated memory
			mem, _ := memlimit.SetGoMemLimit(0.9)
			logging.Infow("Set memory limit by cgroup", "mem_limit", memByteToStr(mem), "system_total_mem", memByteToStr(sysTotalMem))
		} else {
			// the application is not in container environment, set memory limitation to 90% of system total memory
			mem := int64(float64(sysTotalMem) * 0.9)
			debug.SetMemoryLimit(mem)
			logging.Infow("Set memory limit by system total memory", "mem_limit", memByteToStr(mem), "system_total_mem", memByteToStr(sysTotalMem))
		}
	}
}

func memByteToStr[T int64 | uint64](v T) string {
	mb := uint64(v) / 1048576
	if mb < 1024 {
		return fmt.Sprintf("%d MB", mb)
	}
	gb := float64(mb) / 1024.0
	return fmt.Sprintf("%.1f GB", gb)
}
