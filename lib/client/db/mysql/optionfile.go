/*
Copyright 2021 Gravitational, Inc.

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

package mysql

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gravitational/trace"
	"gopkg.in/ini.v1"

	"github.com/gravitational/teleport/api/utils"
	"github.com/gravitational/teleport/lib/client/db/profile"
)

func init() {
	ini.PrettyFormat = false // Pretty format breaks mysql.
}

// OptionFile represents MySQL option file.
//
// https://dev.mysql.com/doc/refman/8.0/en/option-files.html
type OptionFile struct {
	// iniFile is the underlying ini file.
	iniFile *ini.File
	// path is the service file path.
	path string
}

func DefaultConfigPath() (string, error) {
	// Default location is .my.cnf file in the user's home directory.
	usr, err := utils.CurrentUser()
	if err != nil {
		return "", trace.ConvertSystemError(err)
	}
	return filepath.Join(usr.HomeDir, mysqlOptionFile), nil
}

// Load loads MySQL option file from the default location.
func Load() (*OptionFile, error) {
	cnfPath, err := DefaultConfigPath()
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return LoadFromPath(cnfPath)
}

// LoadFromPath loads MySQL option file from the specified path.
func LoadFromPath(path string) (*OptionFile, error) {
	// Loose load will ignore file not found error.
	iniFile, err := ini.LoadSources(ini.LoadOptions{
		Loose:            true,
		AllowBooleanKeys: true,
	}, path)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return &OptionFile{
		iniFile: iniFile,
		path:    path,
	}, nil
}

// Upsert saves the provided connection profile in MySQL option file.
func (o *OptionFile) Upsert(profile profile.ConnectProfile) error {
	sectionName := o.section(profile.Name)
	section := o.iniFile.Section(sectionName)
	if section != nil {
		o.iniFile.DeleteSection(sectionName)
	}
	section, err := o.iniFile.NewSection(sectionName)
	if err != nil {
		return trace.Wrap(err)
	}
	section.NewKey("host", profile.Host)
	section.NewKey("port", strconv.Itoa(profile.Port))
	if profile.User != "" {
		section.NewKey("user", profile.User)
	}
	if profile.Database != "" {
		section.NewKey("database", profile.Database)
	}
	if profile.Insecure {
		section.NewKey("ssl-mode", MySQLSSLModeVerifyCA)
	} else {
		section.NewKey("ssl-mode", MySQLSSLModeVerifyIdentity)
	}
	// On Windows paths will contain \, which must be escaped to \\ as per https://dev.mysql.com/doc/refman/8.0/en/option-files.html
	section.NewKey("ssl-ca", strings.ReplaceAll(profile.CACertPath, `\`, `\\`))
	section.NewKey("ssl-cert", strings.ReplaceAll(profile.CertPath, `\`, `\\`))
	section.NewKey("ssl-key", strings.ReplaceAll(profile.KeyPath, `\`, `\\`))
	return o.iniFile.SaveTo(o.path)
}

// Env returns the specified connection profile as environment variables.
func (o *OptionFile) Env(name string) (map[string]string, error) {
	_, err := o.iniFile.GetSection(o.section(name))
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			return nil, trace.NotFound("connection profile %q not found", name)
		}
		return nil, trace.Wrap(err)
	}
	// Unlike e.g. Postgres, where pretty much every CLI flag has a respective
	// env var, MySQL recognizes only a limited set of variables that doesn't
	// cover the whole set of things we need to configure such as TLS config:
	//
	// https://dev.mysql.com/doc/refman/8.0/en/environment-variables.html
	//
	// Due to this fact, we use the "option group suffix" which makes clients
	// use specific section from ~/.my.cnf file that has all these settings.
	return map[string]string{
		"MYSQL_GROUP_SUFFIX": suffix(name),
	}, nil
}

// Delete removes the specified connection profile.
func (o *OptionFile) Delete(name string) error {
	o.iniFile.DeleteSection(o.section(name))
	return o.iniFile.SaveTo(o.path)
}

// section returns the section name in MySQL option file.
//
// Sections that are read by MySQL client start with "client" prefix.
func (o *OptionFile) section(name string) string {
	return "client" + suffix(name)
}

func suffix(name string) string {
	return "_" + name
}

const (
	// MySQLSSLModeVerifyCA is MySQL SSL mode that verifies server CA.
	//
	// See MySQL SSL mode docs for more info:
	// https://dev.mysql.com/doc/refman/8.0/en/connection-options.html#option_general_ssl-mode
	MySQLSSLModeVerifyCA = "VERIFY_CA"
	// MySQLSSLModeVerifyIdentity is MySQL SSL mode that verifies host name.
	//
	// See MySQL SSL mode docs for more info:
	// https://dev.mysql.com/doc/refman/8.0/en/connection-options.html#option_general_ssl-mode
	MySQLSSLModeVerifyIdentity = "VERIFY_IDENTITY"
	// mysqlOptionFile is the default name of the MySQL option file.
	mysqlOptionFile = ".my.cnf"
)
