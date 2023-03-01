/*
Copyright 2015 Gravitational, Inc.

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

// Package testauthority implements a wrapper around native.Keygen that uses
// pre-computed keys.
package testauthority

import (
	"context"

	"github.com/gravitational/trace"
	"github.com/jonboulle/clockwork"

	"github.com/gravitational/teleport/api/utils/keys"
	"github.com/gravitational/teleport/lib/auth/keygen"
	"github.com/gravitational/teleport/lib/auth/native"
	"github.com/gravitational/teleport/lib/services"
)

type Keygen struct {
	clock clockwork.Clock
	*keygen.Keygen
}

// New creates a new key generator with defaults
func New() *Keygen {
	return NewWithClock(clockwork.NewRealClock())
}

// NewWithClock creates a new key generator with the specified configuration
func NewWithClock(clock clockwork.Clock) *Keygen {
	inner := keygen.New(context.Background(), keygen.SetClock(clock))
	return &Keygen{Keygen: inner, clock: clock}
}

// GeneratePrivateKey generates a new PrivateKey.
func (n *Keygen) GeneratePrivateKey() (*keys.PrivateKey, error) {
	priv, _, err := n.GenerateKeyPair()
	if err != nil {
		return nil, trace.Wrap(err)
	}

	return keys.ParsePrivateKey(priv)
}

func (n *Keygen) GetNewKeyPairFromPool() (priv []byte, pub []byte, err error) {
	return n.GenerateKeyPair()
}

func (n *Keygen) GenerateKeyPair() (priv []byte, pub []byte, err error) {
	return native.GenerateKeyPair()
}

func (n *Keygen) GenerateHostCert(c services.HostCertParams) ([]byte, error) {
	return n.GenerateHostCertWithoutValidation(c)
}

func (n *Keygen) GenerateUserCert(c services.UserCertParams) ([]byte, error) {
	return n.GenerateUserCertWithoutValidation(c)
}

func (n *Keygen) GenerateJWT() (pub []byte, priv []byte, err error) {
	return []byte(`-----BEGIN RSA PUBLIC KEY-----
MIIBCgKCAQEA+Igxw1i29PtAgaXOdJnkpPRaKANbIYvXpXZ3+UZ0MGYEnS01nqVE
gSic9sDPKtPcw0Bj35u6/2TTJpB1BJqYrcMB1ahP2aRzBgomUSV1BPVLI7F7EH6U
TIdk41ZT0qBtpPlUWJEAjmkPEcC8e+4oBpwW+mvdvupVcrTgFFLqzsvx2ger2S89
/IrVPWPoW513Dml5zJMgiWEf5cKyyXtQAtieftQmX5bJ9t4PEmH3+mMCu4WKKNt9
rLkmqva/gU21PHsop4nbjl5Sd8wITJkfvf/okxLIv1YXkg9z7RpbzSfvQSUUp9RR
9n4Y1beA+k6YyMGjUHeRw3PfbKfiDFaRvQIDAQAB
-----END RSA PUBLIC KEY-----`), []byte(`-----BEGIN RSA PRIVATE KEY-----
MIIEpQIBAAKCAQEA+Igxw1i29PtAgaXOdJnkpPRaKANbIYvXpXZ3+UZ0MGYEnS01
nqVEgSic9sDPKtPcw0Bj35u6/2TTJpB1BJqYrcMB1ahP2aRzBgomUSV1BPVLI7F7
EH6UTIdk41ZT0qBtpPlUWJEAjmkPEcC8e+4oBpwW+mvdvupVcrTgFFLqzsvx2ger
2S89/IrVPWPoW513Dml5zJMgiWEf5cKyyXtQAtieftQmX5bJ9t4PEmH3+mMCu4WK
KNt9rLkmqva/gU21PHsop4nbjl5Sd8wITJkfvf/okxLIv1YXkg9z7RpbzSfvQSUU
p9RR9n4Y1beA+k6YyMGjUHeRw3PfbKfiDFaRvQIDAQABAoIBAQCFdN0EAQd91c11
0QtkIZ9d1Lj085hkEyvhdhRGj0alFqMzo6s/XY/Dq4NeHXshjFLnphP7ZyyrKAEa
nEe6CSojZKv/hzoZrOz3OUwKFwsXdoC60fs7iD0qOyo5yYmJeQxnoltgk7ywvEmT
RuPvyJtFsVvFbVbNxhfMWMRbJYthL6Pwxi5yd0gGt1Iyav5AqUojnBCQsPM0NEHI
SyoO0Id0Fqc8j621IlnHhsR6yTQjsOeRX7mn3oBsAVQ/xS7RG5vF9NzJW58PgSiA
4OYDbwzZvUq76AF1CcuTOkoaqiTnvkljvSK60EkmCAB2J5ivOM1VqjfFUREsrg5y
1ZQHNXkhAoGBAP18/TJL/SVpqD/4fJq8ZUk7jGYhcpRMSAOSkTVfQ3wQrJZx1hAD
K75OVUyLKe5R+4uS1VuMrkwK5EBIWhbFjTvcOKQjkWMYQsY/lSvJsyhHImYvnjnQ
cYDb44bKfRZ+iVPnYotxmdZ5aalFS3iEJh7ZXsMnIJ9Twc0Vo0QYN7unAoGBAPr+
ohQc5VGPsGmNUY/+9CDtTBUlnd8hDU/LY2d2A0pW3Pp3yc/LKbNN+o9r8TjgaMe+
FH0AcglXy3hyX1Tx6e6C4ZpaAk1utxpgEVVou12mKYmndyNaH2tgwb1hFbzTdKPV
Ff+ygtki/Eu6urigZZss9bhq61r4k3AzuPjC4GP7AoGBAJRy2iTWc42xbqLn9rD3
m6ljgjldZSiL87CD6R4EiBTj/u6sA9ykvr1YSoPlC81RnaqDdweCP6Cw0DMFLB0h
3DAuK82UNtR9pL1NByL5oD36Sp7lTBg3hgEcxQZvFwpRWEMWwpM/GASOXd6Pgj81
xM1UJzbKd0RXXKup/E8oj5sJAoGAU2rPSSn1WO8NbXcnNVlBn7PeBmUzG9YrS2rI
RblmDI3j8WZgbywRVuNCs+nnCMUkbcYRnx3HyK0iFYzFfEDOQ5PCEP97Jmr2ddCZ
0i31n4E66uH6aYhpStGkciFTDSel61FFd27HqAzFlxGfPv8n5bPCkqEOSXS146N9
BUgXNYMCgYEA3PmxSB3+P8wdozMxWUndrzwol07rNjWJGSMvBa+TPgHqNRQoDByZ
9xs+lyfPZlyk4fBG4Il1AhnMgPH5/eph0ERYVokNH+k3lsIKZ9xorWQXEM6X2tNO
UDjWGmIjGpyTetPVS0OEpVzwTSMg/t5s7QhRNMvfnqPcm0DhY6fB2bA=
-----END RSA PRIVATE KEY-----`), nil
}

type PreparedKeyPair struct {
	Priv []byte
	Pub  []byte
}
