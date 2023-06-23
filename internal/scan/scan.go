package scan

import (
	"math/rand"
	"strconv"
	"time"

	"github.com/doggystylez/penpal/internal/alert"
	"github.com/doggystylez/penpal/internal/config"
	"github.com/doggystylez/penpal/internal/rpc"
)

const signThreshold = 0.95

func Monitor(cfg config.Config) {
	alertChan := make(chan alert.Alert)
	exit := make(chan bool)
	client := rpc.New()
	for _, network := range cfg.Networks {
		go scanNetwork(client, network, alertChan)
		go alert.Watch(alertChan, cfg.Notifiers)
	}
	if cfg.Health.Interval != 0 {
		go healthServer(client, cfg)
		go healthCheck(client.Client, cfg.Health, alertChan)
	}
	<-exit
}

func scanNetwork(client rpc.Client, network config.Network, alertChan chan<- alert.Alert) {
	var (
		interval int
		alerted  bool
	)
	for {
		alertChan <- checkNetwork(client, network, &alerted)
		if alerted && network.Interval > 2 {
			interval = 2
		} else {
			interval = network.Interval
		}
		time.Sleep(time.Duration(interval) * time.Minute)
	}
}

func checkNetwork(client rpc.Client, network config.Network, alerted *bool) alert.Alert {
	var (
		chainId string
		height  string
		err     error
	)
	rpcs := network.Rpcs
	if len(rpcs) > 1 {
		for {
			var i int
			var nRpcs []string
			if len(rpcs) == 0 && !*alerted {
				*alerted = true
				return alert.NoRpc(network.ChainId)
			} else {
				i = rand.Intn(len(rpcs))
				for _, r := range rpcs {
					if r != rpcs[i] {
						nRpcs = append(nRpcs, r)
					}
				}
				client.Url = rpcs[i]
				rpcs = nRpcs
				chainId, height, err = rpc.GetLastestHeight(client)
				if err != nil {
					break
				}
				if chainId == network.ChainId {
					break
				}
			}
		}
	} else if len(rpcs) == 1 {
		client.Url = network.Rpcs[0]
		chainId, height, err = rpc.GetLastestHeight(client)
		if err != nil && !*alerted {
			*alerted = true
			return alert.NoRpc(network.ChainId)
		}
		if chainId != network.ChainId && !*alerted {
			*alerted = true
			return alert.NoRpc(network.ChainId)
		}
	}
	heightInt, _ := strconv.Atoi(height)
	return backCheck(client, network, heightInt, alerted)

}

func backCheck(client rpc.Client, cfg config.Network, height int, alerted *bool) alert.Alert {
	var (
		signed    int
		rpcErrors int
	)
	for checkHeight := height - cfg.BackCheck + 1; checkHeight <= height; checkHeight++ {
		block, err := rpc.GetBlockFromHeight(client, strconv.Itoa(checkHeight))
		if err != nil || block.Error != nil {
			rpcErrors++
			cfg.BackCheck--
			continue
		}
		if checkSig(cfg.Address, block) {
			signed++
		}
	}
	if rpcErrors > cfg.BackCheck && !*alerted {
		*alerted = true
		return alert.RpcDown(client.Url)
	} else if float64(signed)/float64(cfg.BackCheck) < signThreshold {
		*alerted = true
		return alert.Missed((cfg.BackCheck - signed), cfg.BackCheck, cfg.ChainId)
	} else if *alerted {
		*alerted = false
		return alert.Cleared(signed, cfg.BackCheck, cfg.ChainId)
	} else {
		return alert.Nil(signed, cfg.BackCheck, cfg.ChainId)
	}
}

func checkSig(address string, block rpc.Block) bool {
	for _, sig := range block.Result.Block.LastCommit.Signatures {
		if sig.ValidatorAddress == address {
			return true
		}
	}
	return false
}
