package stats

import (
	"bytes"
	"fmt"
	goio "io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"juno/third_party/forked/golang/glog"

	"juno/cmd/proxy/config"
	"juno/cmd/proxy/stats/shmstats"
	"juno/pkg/io"
	"juno/pkg/logging/cal"
	calconfig "juno/pkg/logging/cal/config"
	"juno/pkg/logging/sherlock"
	"juno/pkg/stats"
)

var (
	statslogger = statsLoggerT{chDone: make(chan bool)}

	_ stats.IStatesWriter = (*statsFileWriterT)(nil)
	_ stats.IStatesWriter = (*statsCalWriterT)(nil)
	_ stats.IStatesWriter = (*statsSherlockWriterT)(nil)
)

type (
	statsLoggerT struct {
		writers []stats.IStatesWriter
		//		stats       shmstats.ReqProcStats
		workerStats [][]stats.IState
		chDone      chan bool
	}

	statsFileWriterT struct {
		cnt    int
		header string
		writer goio.WriteCloser
	}
	statsCalWriterT struct {
	}
	statsSherlockWriterT struct {
		dimensions []sherlock.Dims
		count      uint32
	}
)

func InitializeForMonitor(args ...interface{}) (err error) {
	shmstats.InitForMonitor()
	statslogger.Init()
	return
}

func FinalizeForMonitor() {
	shmstats.Finalize()
}

func (l *statsLoggerT) Init() {
	//shmstats must have been initialized

	srvStats := shmstats.GetServerStats()
	numWorkers := int(srvStats.NumWorkers)
	l.workerStats = make([][]stats.IState, numWorkers)
	repTargets := shmstats.GetReplicationTargetStats()
	numTargets := len(repTargets)

	if numWorkers != 0 {
		lsnrs := shmstats.GetListenerStats()
		for i := 0; i < numWorkers; i++ {
			mgr := shmstats.GetWorkerStatsManager(i)
			st := mgr.GetWorkerStatsPtr()

			conns := mgr.GetInboundConnStatsPtr()
			if len(conns) == len(lsnrs) {
				for li, lsnr := range lsnrs {
					var name string
					if io.ListenerType(lsnr.Type) == io.ListenerTypeTCPwSSL {
						name = "ssl_conns"
					} else {
						name = "conns"
					}
					l.workerStats[i] = append(l.workerStats[i], stats.NewUint32State(&conns[li].NumConnections, name, ""))
				}
			}
			if st != nil {
				l.workerStats[i] = append(l.workerStats[i],
					[]stats.IState{
						stats.NewUint32State(&st.RequestsPerSecond, "tps", "number of transactions per second"),
						stats.NewUint32State(&st.AvgReqProcTime, "apt", "average processing time"),
						stats.NewUint32State(&st.ReqProcErrsPerSecond, "eps", "number of errors per second"),
						stats.NewUint32State(&st.NumReads, "nRead", "number of active read requests"),
						stats.NewUint32State(&st.NumWrites, "nWrite", "number of active write requests"),
						stats.NewUint16State(&st.NumBadShards, "nBShd", "number of bad shards"),
						stats.NewUint16State(&st.NumAlertShards, "nAShd", "number of shards with no redundancy"),
						stats.NewUint16State(&st.NumWarnShards, "nWShd", "number of shards with bad SS"),
						stats.NewFloat32State(&st.ProcCpuUsage, "pCPU", "Process CPU usage percentage", 1),
						stats.NewFloat32State(&st.MachCpuUsage, "mCPU", "Machine CPU usage percentage", 1),
					}...)
			}

			// replication stats
			for t := 0; t < numTargets; t++ {
				repStats := mgr.GetReplicatorStatsPtr(t)
				tgtName := string(repTargets[t].Name[:repTargets[t].LenName])
				repconn := fmt.Sprintf("%s_c", tgtName)
				repdrop := fmt.Sprintf("%s_d", tgtName)
				reperr := fmt.Sprintf("%s_e", tgtName)
				l.workerStats[i] = append(l.workerStats[i],
					[]stats.IState{
						stats.NewUint16State(&repStats.NumConnections, repconn, "replication connection count"),
						stats.NewUint64DeltaState(&repStats.NumDrops, repdrop,
							"replication requests drop count", uint16(sherlock.ShrLockConfig.Resolution)),
						stats.NewUint64DeltaState(&repStats.NumErrors, reperr,
							"replication requests error count", uint16(sherlock.ShrLockConfig.Resolution)),
					}...)
			}
		}
		cfg := &config.Conf
		if cfg.StateLogEnabled {
			if _, err := os.Stat(cfg.StateLogDir); os.IsNotExist(err) {
				os.Mkdir(cfg.StateLogDir, 0777)
			}
		}

		statelogName := filepath.Join(cfg.StateLogDir, "state.log")
		l.writers = nil
		if file, err := os.OpenFile(statelogName, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err == nil {
			var buf bytes.Buffer
			for _, i := range statslogger.workerStats[0] {
				format := fmt.Sprintf("%%%ds ", i.Width())
				fmt.Fprintf(&buf, format, i.Header())
			}

			l.writers = append(l.writers, &statsFileWriterT{
				writer: file,
				header: fmt.Sprintf("%3s %s", "id", string(buf.Bytes())),
			})
		} else {
			return
		}
		if cal.IsEnabled() {
			l.writers = append(l.writers, &statsCalWriterT{})
		}
		/*
			l.writers = []stats.IStatesWriter{
				,
			}
		*/
		if sherlock.IsEnabled() {
			sw := statsSherlockWriterT{}
			sw.dimensions = make([]sherlock.Dims, numWorkers)
			for i := 0; i < numWorkers; i++ {
				sw.dimensions[i] = sherlock.Dims{sherlock.GetDimName(): calconfig.CalConfig.Poolname, "id": fmt.Sprintf("%d", i)}
			}
			l.writers = append(l.writers, &sw)
		}
	}
}

func (l *statsLoggerT) DoWrite() {
	ticker := time.NewTicker(1 * time.Second)
	defer func() {
		ticker.Stop()
		for _, w := range l.writers {
			w.Close()
		}
	}()
	for {
		select {
		case <-l.chDone:
			return
		case now := <-ticker.C:
			for _, w := range l.writers {
				w.Write(now)
			}
		}
	}
}

func (w *statsFileWriterT) Write(now time.Time) error {
	numWorkers := len(statslogger.workerStats)

	if numWorkers != 0 {
		for wi := 0; wi < numWorkers; wi++ {
			var buf bytes.Buffer
			for _, i := range statslogger.workerStats[wi] {
				format := fmt.Sprintf("%%%ds ", i.Width())
				fmt.Fprintf(&buf, format, i.State())
			}
			if w.cnt%23 == 0 {
				fmt.Fprintf(w.writer, "%s %s\n", now.Format("01-02 15:04:05"), w.header)
			}
			fmt.Fprintf(w.writer, "%s %3d %s\n", now.Format("01-02 15:04:05"), wi, string(buf.Bytes()))
			w.cnt++
		}
	}
	return nil
}

func (w *statsFileWriterT) Close() error {
	if w.writer != nil {
		return w.writer.Close()
	}
	return nil
}

func (w *statsCalWriterT) Write(now time.Time) error {
	if cal.IsEnabled() {
		numWorkers := len(statslogger.workerStats)
		for wi := 0; wi < numWorkers; wi++ {
			var buf bytes.Buffer
			for i, v := range statslogger.workerStats[wi] {
				if i != 0 {
					buf.WriteByte('&')
				}
				fmt.Fprintf(&buf, "%s=%s", v.Header(), v.State())
			}
			cal.StateLog(fmt.Sprintf("%d", wi), buf.Bytes())
		}
	}
	return nil

}
func (w *statsCalWriterT) Close() error {
	return nil
}

var sherlockHeaderKeyMap = map[string]string{
	"free":       "free_mb_storage_space",
	"used":       "storage_used_mb",
	"req":        "requestCount",
	"apt":        "latency_avg_us",
	"Read":       "read_count",
	"PC":         "prepare_create_count",
	"PU":         "prepare_update_count",
	"PS":         "prepare_set_count",
	"D":          "delete_count",
	"PD":         "prepare_delete_count",
	"C":          "commit_count",
	"A":          "abort_count",
	"RR":         "repair_count",
	"tps":        "requestCountPerSec",
	"eps":        "errorPerSec",
	"nRead":      "read_count",
	"nWrite":     "write_count",
	"nBadShds":   "shard_bad_count",
	"nWarnShds":  "shard_warning_count",
	"nAlertShds": "shard_alert_count",
	"ssl_conns":  "conns_ssl_count",
	"conns":      "conns_count",
	"pCPU":       "cpu_usage",
	"mCPU":       "machine_cpu_usage",
}

func (w *statsSherlockWriterT) Write(now time.Time) error {
	if sherlock.IsEnabled() {
		if w.count%sherlock.ShrLockConfig.Resolution == 0 {
			numWorkers := len(statslogger.workerStats)
			for wi := 0; wi < numWorkers; wi++ {
				for _, v := range statslogger.workerStats[wi] {
					if fl, err := strconv.ParseFloat(v.State(), 64); err == nil {
						w.sendMetricsData(wi, v.Header(), fl, now)
					}
				}
			}
		}
		w.count++
	}
	return nil
}

func (w *statsSherlockWriterT) sendMetricsData(wid int, key string, value float64, now time.Time) {
	var data [1]sherlock.FrontierData
	headerKey, ok := sherlockHeaderKeyMap[key]
	if !ok {
		headerKey = key
	}
	data[0].Name = headerKey
	data[0].Value = value
	data[0].MetricType = sherlock.Gauge
	err := sherlock.SherlockClient.SendMetric(w.dimensions[wid], data[:1], now)
	if err != nil {
		glog.Debugf("failed to send metric, err=%s", err.Error())
	}
}

func (w *statsSherlockWriterT) Close() error {
	return nil
}

func RunMonitorLogger() {
	go statslogger.DoWrite()
}