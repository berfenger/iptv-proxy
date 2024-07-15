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
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"

	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/jamesnetherton/m3u"
)

type TrackConfig struct {
	track        *m3u.Track
	relativePath string
}

func (c *Config) routes(r *gin.RouterGroup) {
	r = r.Group(c.CustomEndpoint)

	//Xtream service endopoints
	if c.ProxyConfig.XtreamBaseURL != "" {
		c.xtreamRoutes(r)
		if strings.Contains(c.XtreamBaseURL, c.RemoteURL.Host) &&
			c.XtreamUser.String() == c.RemoteURL.Query().Get("username") &&
			c.XtreamPassword.String() == c.RemoteURL.Query().Get("password") {

			r.GET("/"+c.M3UFileName, c.authenticate, c.xtreamGetAuto)
			// XXX Private need: for external Android app
			r.POST("/"+c.M3UFileName, c.authenticate, c.xtreamGetAuto)

			return
		}
	}

	c.m3uRoutes(r)
}

func (c *Config) xtreamRoutes(r *gin.RouterGroup) {
	getphp := gin.HandlerFunc(c.xtreamGet)
	if c.XtreamGenerateApiGet {
		getphp = c.xtreamApiGet
	}
	r.GET("/get.php", c.authenticate, getphp)
	r.POST("/get.php", c.authenticate, getphp)
	r.GET("/apiget", c.authenticate, c.xtreamApiGet)
	r.GET("/player_api.php", c.authenticate, c.xtreamPlayerAPIGET)
	r.POST("/player_api.php", c.appAuthenticate, c.xtreamPlayerAPIPOST)
	r.GET("/xmltv.php", c.authenticate, c.xtreamXMLTV)
	r.GET(fmt.Sprintf("/%s/%s/:id", c.User, c.Password), c.xtreamStreamHandler)
	r.GET(fmt.Sprintf("/live/%s/%s/:id", c.User, c.Password), c.xtreamStreamLive)
	r.GET(fmt.Sprintf("/timeshift/%s/%s/:duration/:start/:id", c.User, c.Password), c.xtreamStreamTimeshift)
	r.GET(fmt.Sprintf("/movie/%s/%s/:id", c.User, c.Password), c.xtreamStreamMovie)
	r.GET(fmt.Sprintf("/series/%s/%s/:id", c.User, c.Password), c.xtreamStreamSeries)
	r.GET(fmt.Sprintf("/hlsr/:token/%s/%s/:channel/:hash/:chunk", c.User, c.Password), c.xtreamHlsrStream)
	r.GET("/hls/:token/:chunk", c.xtreamHlsStream)
	r.GET("/play/:token/:type", c.xtreamStreamPlay)
}

func (c *Config) m3uRoutes(r *gin.RouterGroup) {
	r.GET("/"+c.M3UFileName, c.authenticate, c.getM3U)
	// XXX Private need: for external Android app
	r.POST("/"+c.M3UFileName, c.authenticate, c.getM3U)

	for i, trackConfig := range c.trackConfig {

		if strings.HasSuffix(trackConfig.track.URI, ".m3u8") {
			r.GET(fmt.Sprintf("/%s/%s/%s/%d/:id", c.endpointAntiColision, c.User.PathEscape(), c.Password.PathEscape(), i), trackConfig.m3u8ReverseProxy)
		} else {
			r.GET(trackConfig.relativePath, trackConfig.reverseProxy)
		}
	}
}

func computeRelPathForPlaylist(c *Config) {
	var hashes []string
	var result []*TrackConfig

	for i, track := range c.playlist.Tracks {
		var relativePath string

		if strings.HasSuffix(track.URI, ".m3u8") {
			relativePath = fmt.Sprintf("/%s/%s/%s/%d/:id", c.endpointAntiColision, c.User.PathEscape(), c.Password.PathEscape(), i)
		} else {
			u, err := url.Parse(track.URI)
			var id string
			hash := hashByMethod(c.ProxyConfig.URLHashMethod, track)
			if hash != nil {
				if slices.Contains(hashes, *hash) {
					id = string2HexHash(track.URI)
				} else {
					hashes = append(hashes, *hash)
					id = *hash
				}
			} else {
				id = strconv.Itoa(i)
			}
			if err == nil {
				relativePath = fmt.Sprintf("/%s/%s/%s/%s/%s", c.endpointAntiColision, c.User.PathEscape(), c.Password.PathEscape(), id, path.Base(u.Path))
			} else {
				relativePath = fmt.Sprintf("/%s/%s/%s/%s/%s", c.endpointAntiColision, c.User.PathEscape(), c.Password.PathEscape(), id, path.Base(track.URI))
			}
		}
		trackConfig := &TrackConfig{
			track:        &c.playlist.Tracks[i],
			relativePath: relativePath,
		}
		result = append(result, trackConfig)
	}
	c.trackConfig = result
}

func hashByMethod(hashMethod string, track m3u.Track) *string {
	switch {
	case hashMethod == "url":
		return hashMethodURL(track)
	case hashMethod == "id":
		return applyFirst([]func(m3u.Track) *string{hashMethodID, hashMethodURL}, track)
	case hashMethod == "tags":
		return applyFirst([]func(m3u.Track) *string{hashMethodTags, hashMethodURL}, track)
	case hashMethod == "smart":
		return applyFirst([]func(m3u.Track) *string{hashMethodID, hashMethodTags, hashMethodURL}, track)
	default:
		return nil
	}
}

func hashMethodURL(track m3u.Track) *string {
	hex := string2HexHash(track.URI)
	return &hex
}

func hashMethodID(track m3u.Track) *string {
	u, err := url.Parse(track.URI)
	if err != nil {
		id := u.Query().Get("id")
		if id != "" {
			hex := string2HexHash(id)
			return &hex
		}
	}
	return nil
}

func hashMethodTags(track m3u.Track) *string {
	if len(track.Tags) > 0 {
		sort.Slice(track.Tags, func(i, j int) bool {
			return track.Tags[i].Name < track.Tags[j].Name
		})
		var sb strings.Builder
		for _, t := range track.Tags {
			sb.WriteString(t.Name)
			sb.WriteString("=")
			sb.WriteString(t.Value)
			sb.WriteString(",")
		}
		hex := string2HexHash(sb.String())
		return &hex
	}
	return nil
}

func applyFirst[A, B any](fns []func(A) *B, a A) *B {
	for _, fn := range fns {
		fa := fn(a)
		if fa != nil {
			return fa
		}
	}
	return nil
}

func string2HexHash(s string) string {
	hash := md5.Sum([]byte(s))
	return strings.ToLower(hex.EncodeToString(hash[:]))
}
