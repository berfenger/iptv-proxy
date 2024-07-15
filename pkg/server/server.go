/*
 * Iptv-Proxy is a project to proxyfie an m3u file and to proxyfie an Xtream iptv service (client API).
 * Copyright (C) 2020  Pierre-Emmanuel Jacquier
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <https://www.gnu.org/licenses/>.
 */

package server

import (
	"bytes"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/jamesnetherton/m3u"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
	uuid "github.com/satori/go.uuid"

	"github.com/gin-gonic/gin"
)

var defaultProxyfiedM3UPath = filepath.Join(os.TempDir(), uuid.NewV4().String()+".iptv-proxy.m3u")
var endpointAntiColision = strings.Split(uuid.NewV4().String(), "-")[0]

// Config represent the server configuration
type Config struct {
	*config.ProxyConfig

	// M3U service part
	playlist *m3u.Playlist

	trackConfig []*TrackConfig

	// path to the proxyfied m3u file
	proxyfiedM3UPath string

	endpointAntiColision string
}

// NewServer initialize a new server configuration
func NewServer(config *config.ProxyConfig) (*Config, error) {
	var p m3u.Playlist
	if config.RemoteURL.String() != "" {
		var err error
		p, err = m3u.Parse(config.RemoteURL.String())
		if err != nil {
			return nil, err
		}
		p = trimTagStrings(p)
	}

	if trimmedCustomId := strings.Trim(config.CustomId, "/"); trimmedCustomId != "" {
		endpointAntiColision = trimmedCustomId
	}

	return &Config{
		config,
		&p,
		nil,
		defaultProxyfiedM3UPath,
		endpointAntiColision,
	}, nil
}

// Serve the iptv-proxy api
func (c *Config) Serve() error {
	if err := c.playlistInitialization(); err != nil {
		return err
	}

	router := gin.Default()
	router.Use(cors.Default())
	group := router.Group("/")
	c.routes(group)

	return router.Run(fmt.Sprintf(":%d", c.HostConfig.Port))
}

func (c *Config) playlistInitialization() error {
	if len(c.playlist.Tracks) == 0 {
		return nil
	}

	computeRelPathForPlaylist(c)

	f, err := os.Create(c.proxyfiedM3UPath)
	if err != nil {
		return err
	}
	defer f.Close()

	return c.marshallInto(f, false)
}

// MarshallInto a *bufio.Writer a Playlist.
func (c *Config) marshallInto(into *os.File, xtream bool) error {
	filteredTrack := make([]m3u.Track, 0, len(c.playlist.Tracks))

	ret := 0
	into.WriteString("#EXTM3U\n") // nolint: errcheck
	for _, trackConfig := range c.trackConfig {
		var buffer bytes.Buffer

		buffer.WriteString("#EXTINF:")                                   // nolint: errcheck
		buffer.WriteString(fmt.Sprintf("%d ", trackConfig.track.Length)) // nolint: errcheck
		for i := range trackConfig.track.Tags {
			if i == len(trackConfig.track.Tags)-1 {
				buffer.WriteString(fmt.Sprintf("%s=%q", trackConfig.track.Tags[i].Name, trackConfig.track.Tags[i].Value)) // nolint: errcheck
				continue
			}
			buffer.WriteString(fmt.Sprintf("%s=%q ", trackConfig.track.Tags[i].Name, trackConfig.track.Tags[i].Value)) // nolint: errcheck
		}

		uri, err := c.replaceURL(trackConfig.track.URI, trackConfig, xtream)
		if err != nil {
			ret++
			log.Printf("ERROR: track: %s: %s", trackConfig.track.Name, err)
			continue
		}

		into.WriteString(fmt.Sprintf("%s, %s\n%s\n", buffer.String(), trackConfig.track.Name, uri)) // nolint: errcheck

		filteredTrack = append(filteredTrack, *trackConfig.track)
	}
	c.playlist.Tracks = filteredTrack

	return into.Sync()
}

// ReplaceURL replace original playlist url by proxy url
func (c *Config) replaceURL(uri string, trackConfig *TrackConfig, xtream bool) (string, error) {
	oriURL, err := url.Parse(uri)
	if err != nil {
		return "", err
	}

	protocol := "http"
	if c.HTTPS {
		protocol = "https"
	}

	customEnd := strings.Trim(c.CustomEndpoint, "/")
	if customEnd != "" {
		customEnd = fmt.Sprintf("/%s", customEnd)
	}

	uriPath := oriURL.EscapedPath()
	if xtream {
		uriPath = strings.ReplaceAll(uriPath, c.XtreamUser.PathEscape(), c.User.PathEscape())
		uriPath = strings.ReplaceAll(uriPath, c.XtreamPassword.PathEscape(), c.Password.PathEscape())
	} else {
		uriPath = trackConfig.relativePath
	}

	basicAuth := oriURL.User.String()
	if basicAuth != "" {
		basicAuth += "@"
	}

	newURI := fmt.Sprintf(
		"%s://%s%s:%d%s%s",
		protocol,
		basicAuth,
		c.HostConfig.Hostname,
		c.AdvertisedPort,
		customEnd,
		uriPath,
	)

	newURL, err := url.Parse(newURI)
	if err != nil {
		return "", err
	}

	return newURL.String(), nil
}

func trimTagStrings(p m3u.Playlist) m3u.Playlist {
	var newTracks []m3u.Track
	for _, track := range p.Tracks {
		var newTags []m3u.Tag
		for _, tag := range track.Tags {
			trimmedTag := m3u.Tag{
				Name:  strings.TrimSpace(tag.Name),
				Value: strings.TrimSpace(tag.Value),
			}
			newTags = append(newTags, trimmedTag)
		}
		track.Tags = newTags
		newTracks = append(newTracks, track)
	}
	return m3u.Playlist{
		Tracks: newTracks,
	}
}
