///go:build ignore

/*

 Copyright 2022 Gravitational, Inc.

 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.


*/

package main

import (
	"os"
	"strings"

	"github.com/go-piv/piv-go/piv"
	"github.com/gravitational/trace"
	log "github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
)

func main() {
	if err := innerMain(); err != nil {
		log.Fatal(err)
	}
}

func innerMain() error {
	// List all smartcards connected to the system.
	cards, err := piv.Cards()
	if err != nil {
		return trace.Wrap(err)
	}

	// Find a YubiKey and open the reader.
	var yk *piv.YubiKey
	for _, card := range cards {
		if strings.Contains(strings.ToLower(card), "yubikey") {
			yk, err = piv.Open(card)
			if err != nil {
				log.Error("YK error: %+v\n", err)
				continue
			}
			log.Infof("YT: %+v\n", yk)
		}
	}
	if yk == nil {
		log.Fatal("didn't find any YK")
	}

	//Generate a private key on the YubiKey.
	key := piv.Key{
		Algorithm:   piv.AlgorithmRSA2048,
		PINPolicy:   piv.PINPolicyNever,
		TouchPolicy: piv.TouchPolicyNever,
	}
	pub, err := yk.GenerateKey(piv.DefaultManagementKey, piv.SlotAuthentication, key)
	if err != nil {
		return trace.Wrap(err)
	}

	sshPubKey, err := ssh.NewPublicKey(pub)
	if err != nil {
		return trace.Wrap(err)
	}

	if err := os.WriteFile("/Users/jnyckowski/.tsh/yk.pub", ssh.MarshalAuthorizedKey(sshPubKey), 0644); err != nil {
		return trace.Wrap(err)
	}

	return nil
}
