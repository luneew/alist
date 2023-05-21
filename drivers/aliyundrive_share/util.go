package aliyundrive_share

import (
	"context"
	"errors"
	"fmt"
	"github.com/alist-org/alist/v3/drivers/aliyundrive_open"
	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/pkg/cron"
	"github.com/alist-org/alist/v3/pkg/utils"
	"os"
	"time"

	"github.com/alist-org/alist/v3/drivers/base"
	"github.com/alist-org/alist/v3/internal/op"
	log "github.com/sirupsen/logrus"
)

var (
	OpenAliyunDriver driver.Driver
	CacheConfig      Config
	CacheConfigPath  string
)

func InitConfig() {
	// 读取config
	CacheConfigPath = os.Getenv("CACHE_CONFIG_PATH")
	if CacheConfigPath == "" {
		CacheConfigPath = "/opt/alist/data/cache.json"
	}
	log.Infof("cache config file dir: %s", CacheConfigPath)
	file, _ := os.ReadFile(CacheConfigPath)
	CacheConfig = Config{}
	_ = utils.Json.Unmarshal(file, &CacheConfig)
	if CacheConfig.OauthTokenURL == "" {
		CacheConfig.OauthTokenURL = "https://api.nn.ci/alist/ali_open/token"
	}

	if CacheConfig.RemoveWay == "" {
		CacheConfig.RemoveWay = "delete"
	}

	var err error
	OpenAliyunDriver, err = getOpenDriver(CacheConfig)

	if err != nil {
		log.Error("failed to init aliyun shard cache open driver")
	}

	log.Infof("start to init aliyun shard cache open driver")

	_, _, err = refreshToken()
	if err != nil {
		log.Errorf("%+v", err)
	}

	refreshCron := cron.NewCron(time.Hour * 2)
	refreshCron.Do(func() {
		_, _, err := refreshToken()
		if err != nil {
			log.Errorf("%+v", err)
		}
	})
}

func getOpenDriver(config Config) (driver.Driver, error) {
	// 生成驱动
	aliOpenAddition := aliyundrive_open.Addition{
		RefreshToken:   config.OpenRefreshToken,
		OrderBy:        config.OrderBy,
		OrderDirection: config.OrderDirection,
		OauthTokenURL:  config.OauthTokenURL,
		ClientID:       config.ClientID,
		ClientSecret:   config.ClientSecret,
		RemoveWay:      config.RemoveWay,
		InternalUpload: config.InternalUpload,
	}
	driveNew, err := op.GetDriverNew("AliyundriveOpen")
	if err != nil {
		return nil, err
	}
	aliyundriveOpen := driveNew()
	aliOpenAdditionJson, err := utils.Json.MarshalToString(aliOpenAddition)
	if err != nil {
		return nil, err
	}
	err = utils.Json.UnmarshalFromString(aliOpenAdditionJson, aliyundriveOpen.GetAddition())
	if err == nil {
		var ctx context.Context
		err = aliyundriveOpen.Init(ctx)
	}
	return aliyundriveOpen, nil
}

func refreshToken() (string, string, error) {
	url := "https://auth.aliyundrive.com/v2/account/token"
	var resp base.TokenResp
	var e ErrorResp
	_, err := base.RestyClient.R().
		SetBody(base.Json{"refresh_token": CacheConfig.SharedRefreshToken, "grant_type": "refresh_token"}).
		SetResult(&resp).
		SetError(&e).
		Post(url)
	if err != nil {
		return "", "", err
	}
	if e.Code != "" {
		return "", "", fmt.Errorf("failed to refresh shared driver token: %s", e.Message)
	}
	CacheConfig.SharedRefreshToken, CacheConfig.SharedAccessToken = resp.RefreshToken, resp.AccessToken
	CacheConfig.OpenRefreshToken = OpenAliyunDriver.(*aliyundrive_open.AliyundriveOpen).RefreshToken

	cacheConfigJson, err := utils.Json.MarshalToString(CacheConfig)
	if err != nil {
		return "", "", err
	}

	if err := os.WriteFile(CacheConfigPath, []byte(cacheConfigJson), 0666); err != nil {
		log.Error("failed to save cache config to config file")
	}
	log.Infof("refresh shared driver token successfully")

	return CacheConfig.SharedRefreshToken, CacheConfig.SharedAccessToken, err
}

func (d *AliyundriveShare) refreshToken() error {
	d.RefreshToken, d.AccessToken = CacheConfig.SharedRefreshToken, CacheConfig.SharedAccessToken
	op.MustSaveDriverStorage(d)
	return nil
}

// do others that not defined in Driver interface
func (d *AliyundriveShare) getShareToken() error {
	data := base.Json{
		"share_id": d.ShareId,
	}
	if d.SharePwd != "" {
		data["share_pwd"] = d.SharePwd
	}
	var e ErrorResp
	var resp ShareTokenResp
	_, err := base.RestyClient.R().
		SetResult(&resp).SetError(&e).SetBody(data).
		Post("https://api.aliyundrive.com/v2/share_link/get_share_token")
	if err != nil {
		return err
	}
	if e.Code != "" {
		return errors.New(e.Message)
	}
	d.ShareToken = resp.ShareToken
	return nil
}

func (d *AliyundriveShare) request(url, method string, callback base.ReqCallback) ([]byte, error) {
	var e ErrorResp
	req := base.RestyClient.R().
		SetError(&e).
		SetHeader("content-type", "application/json").
		SetHeader("Authorization", "Bearer\t"+d.AccessToken).
		SetHeader("x-share-token", d.ShareToken)
	if callback != nil {
		callback(req)
	} else {
		req.SetBody("{}")
	}
	resp, err := req.Execute(method, url)
	if err != nil {
		return nil, err
	}
	if e.Code != "" {
		if e.Code == "AccessTokenInvalid" || e.Code == "ShareLinkTokenInvalid" {
			if e.Code == "AccessTokenInvalid" {
				err = d.refreshToken()
			} else {
				err = d.getShareToken()
			}
			if err != nil {
				return nil, err
			}
			return d.request(url, method, callback)
		} else {
			return nil, errors.New(e.Code + ": " + e.Message)
		}
	}
	return resp.Body(), nil
}

func (d *AliyundriveShare) getFiles(fileId string) ([]File, error) {
	files := make([]File, 0)
	data := base.Json{
		"image_thumbnail_process": "image/resize,w_160/format,jpeg",
		"image_url_process":       "image/resize,w_1920/format,jpeg",
		"limit":                   100,
		"order_by":                d.OrderBy,
		"order_direction":         d.OrderDirection,
		"parent_file_id":          fileId,
		"share_id":                d.ShareId,
		"video_thumbnail_process": "video/snapshot,t_1000,f_jpg,ar_auto,w_300",
		"marker":                  "first",
	}
	for data["marker"] != "" {
		if data["marker"] == "first" {
			data["marker"] = ""
		}
		var e ErrorResp
		var resp ListResp
		res, err := base.RestyClient.R().
			SetHeader("x-share-token", d.ShareToken).
			SetResult(&resp).SetError(&e).SetBody(data).
			Post("https://api.aliyundrive.com/adrive/v3/file/list")
		if err != nil {
			return nil, err
		}
		log.Debugf("aliyundrive share get files: %s", res.String())
		if e.Code != "" {
			if e.Code == "AccessTokenInvalid" || e.Code == "ShareLinkTokenInvalid" {
				err = d.getShareToken()
				if err != nil {
					return nil, err
				}
				return d.getFiles(fileId)
			}
			return nil, errors.New(e.Message)
		}
		data["marker"] = resp.NextMarker
		files = append(files, resp.Items...)
	}
	if len(files) > 0 && d.DriveId == "" {
		d.DriveId = files[0].DriveId
	}
	return files, nil
}
