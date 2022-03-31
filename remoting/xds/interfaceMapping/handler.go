/*
 * Licensed to the Apache Software Foundation (ASF) under one or more
 * contributor license agreements.  See the NOTICE file distributed with
 * this work for additional information regarding copyright ownership.
 * The ASF licenses this file to You under the Apache License, Version 2.0
 * (the "License"); you may not use this file except in compliance with
 * the License.  You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package interfaceMapping

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"sync"
	"time"
)

import (
	structpb "github.com/golang/protobuf/ptypes/struct"
)

import (
	"dubbo.apache.org/dubbo-go/v3/common/logger"
	"dubbo.apache.org/dubbo-go/v3/remoting/xds/common"
	"dubbo.apache.org/dubbo-go/v3/xds/client"
)

const (
	authorizationHeader = "Authorization"
	istiodTokenPrefix   = "Bearer "
)

type InterfaceMapHandlerImpl struct {
	hostAddr common.Addr

	istioDebugAddr common.Addr

	xdsClient client.XDSClient

	istioTokenPath string

	/*
		interfaceAppNameMap store map of serviceUniqueKey -> hostAddr
	*/
	interfaceAppNameMap     map[string]string
	interfaceAppNameMapLock sync.RWMutex

	/*
		interfaceNameHostAddrMap cache the dubbo interface unique key -> hostName
		the data is read from istiod:8080/debug/adsz, connection metadata["LABELS"]["DUBBO_GO"]
	*/
	interfaceNameHostAddrMap     map[string]string
	interfaceNameHostAddrMapLock sync.RWMutex
}

func (i *InterfaceMapHandlerImpl) UnRegister(serviceUniqueKey string) error {
	i.interfaceAppNameMapLock.Lock()
	delete(i.interfaceAppNameMap, serviceUniqueKey)
	i.interfaceAppNameMapLock.Unlock()
	return i.xdsClient.SetMetadata(i.interfaceAppNameMap2DubboGoMetadata())
}

func (i *InterfaceMapHandlerImpl) Register(serviceUniqueKey string) error {
	i.interfaceAppNameMapLock.Lock()
	i.interfaceAppNameMap[serviceUniqueKey] = i.hostAddr.String()
	i.interfaceAppNameMapLock.Unlock()
	return i.xdsClient.SetMetadata(i.interfaceAppNameMap2DubboGoMetadata())
}

func (i *InterfaceMapHandlerImpl) GetHostAddrMap(serviceUniqueKey string) (string, error) {
	i.interfaceNameHostAddrMapLock.RLock()
	if hostAddr, ok := i.interfaceNameHostAddrMap[serviceUniqueKey]; ok {
		return hostAddr, nil
	}
	i.interfaceNameHostAddrMapLock.RUnlock()

	for {
		if interfaceHostAddrMap, err := i.getServiceUniqueKeyHostAddrMapFromPilot(); err != nil {
			return "", err
		} else {
			i.interfaceNameHostAddrMapLock.Lock()
			i.interfaceNameHostAddrMap = interfaceHostAddrMap
			i.interfaceNameHostAddrMapLock.Unlock()
			hostName, ok := interfaceHostAddrMap[serviceUniqueKey]
			if !ok {
				logger.Infof("[XDS Wrapped Client] Try getting interface %s 's host from istio %d:8080\n", serviceUniqueKey, i.istioDebugAddr)
				time.Sleep(time.Millisecond * 100)
				continue
			}
			return hostName, nil
		}
	}
}

// getServiceUniqueKeyHostAddrMapFromPilot get map of service key like 'provider::api.Greeter' to host addr like
// 'dubbo-go-app.default.svc.cluster.local:20000'
func (i *InterfaceMapHandlerImpl) getServiceUniqueKeyHostAddrMapFromPilot() (map[string]string, error) {
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/debug/adsz", i.istioDebugAddr.String()), nil)
	token, err := os.ReadFile(i.istioTokenPath)
	if err != nil {
		return nil, err
	}
	req.Header.Add(authorizationHeader, istiodTokenPrefix+string(token))
	rsp, err := http.DefaultClient.Do(req)
	if err != nil {
		logger.Infof("[XDS Wrapped Client] Try getting interface host map from istio IP %s with error %s\n",
			i.istioDebugAddr, err)
		return nil, err
	}

	data, err := ioutil.ReadAll(rsp.Body)
	if err != nil {
		return nil, err
	}
	adszRsp := &ADSZResponse{}
	if err := json.Unmarshal(data, adszRsp); err != nil {
		return nil, err
	}
	return adszRsp.GetMap(), nil
}

func (i *InterfaceMapHandlerImpl) interfaceAppNameMap2DubboGoMetadata() *structpb.Struct {
	i.interfaceAppNameMapLock.RLock()
	defer i.interfaceAppNameMapLock.RUnlock()
	data, _ := json.Marshal(i.interfaceAppNameMap)
	return GetDubboGoMetadata(string(data))
}

func NewInterfaceMapHandlerImpl(xdsClient client.XDSClient, istioTokenPath string, istioDebugAddr, hostAddr common.Addr) InterfaceMapHandler {
	return &InterfaceMapHandlerImpl{
		xdsClient:                xdsClient,
		interfaceAppNameMap:      map[string]string{},
		interfaceNameHostAddrMap: map[string]string{},
		istioDebugAddr:           istioDebugAddr,
		hostAddr:                 hostAddr,
		istioTokenPath:           istioTokenPath,
	}
}

type InterfaceMapHandler interface {
	Register(string) error
	UnRegister(string) error
	GetHostAddrMap(string) (string, error)
}
