package aliyundrive_share

import (
	"context"
	"github.com/alist-org/alist/v3/drivers/aliyundrive_open"
	"net/http"
	"sync"
	"time"

	"github.com/alist-org/alist/v3/drivers/base"
	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/errs"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/pkg/cron"
	"github.com/alist-org/alist/v3/pkg/utils"
	"github.com/go-resty/resty/v2"
	log "github.com/sirupsen/logrus"
)

var queue = make([]string, 0)

var lock sync.Mutex

type AliyundriveShare struct {
	model.Storage
	Addition
	AccessToken string
	ShareToken  string
	DriveId     string
	cron        *cron.Cron

	limitList func(ctx context.Context, dir model.Obj) ([]model.Obj, error)
	limitLink func(ctx context.Context, file model.Obj) (*model.Link, error)
}

func (d *AliyundriveShare) Config() driver.Config {
	return config
}

func (d *AliyundriveShare) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *AliyundriveShare) Init(ctx context.Context) error {
	err := d.refreshToken()
	if err != nil {
		return err
	}
	err = d.getShareToken()
	if err != nil {
		return err
	}
	d.cron = cron.NewCron(time.Hour * 2)
	d.cron.Do(func() {
		err := d.refreshToken()
		if err != nil {
			log.Errorf("%+v", err)
		}
	})
	d.limitList = utils.LimitRateCtx(d.list, time.Second/4)
	d.limitLink = utils.LimitRateCtx(d.link, time.Second)
	return nil
}

func (d *AliyundriveShare) Drop(ctx context.Context) error {
	if d.cron != nil {
		d.cron.Stop()
	}
	d.DriveId = ""
	return nil
}

func (d *AliyundriveShare) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	return d.limitList(ctx, dir)
}

func (d *AliyundriveShare) list(ctx context.Context, dir model.Obj) ([]model.Obj, error) {
	files, err := d.getFiles(dir.GetID())
	if err != nil {
		utils.Log.Errorf("failed to list file from dir:  %s, error: %v", dir.GetPath(), err)
		return nil, err
	}
	return utils.SliceConvert(files, func(src File) (model.Obj, error) {
		return fileToObj(src), nil
	})
}

func (d *AliyundriveShare) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	return d.limitLink(ctx, file)
}

func (d *AliyundriveShare) link(ctx context.Context, file model.Obj) (*model.Link, error) {
	//todo 计算容量，如果容量不够，清理最老的文件，保持一定数量的窗口，删除最老的文件

	lock.Lock()
	defer func() {
		lock.Unlock()
	}()
	newFileObj, ok := CacheTempFile[file.GetID()]
	// If the key exists
	if ok {
		utils.Log.Infof("file in cache, use cache, file_id:  %s  file:  %s", file.GetID(), file.GetName())
		return OpenAliyunDriver.Link(ctx, newFileObj, model.LinkArgs{})
	}

	// 转存文件
	utils.Log.Infof("start to add cache file, file_id:  %s  file:  %s", file.GetID(), file.GetName())

	fileCopyReq := FileCopyReq{Requests: []Request{
		{
			Body: struct {
				FileID         string `json:"file_id"`
				ShareID        string `json:"share_id"`
				AutoRename     bool   `json:"auto_rename"`
				ToParentFileID string `json:"to_parent_file_id"`
				ToDriveID      string `json:"to_drive_id"`
			}{
				FileID:         file.GetID(),
				ShareID:        d.ShareId,
				AutoRename:     true,
				ToParentFileID: CacheConfig.TempFolderId,
				ToDriveID:      OpenAliyunDriver.(*aliyundrive_open.AliyundriveOpen).DriveId,
			},
			Headers: struct {
				ContentType string `json:"Content-Type"`
			}{
				ContentType: "application/json",
			},
			ID:     "0",
			Method: "POST",
			URL:    "/file/copy",
		},
	},
		Resource: "file",
	}

	var fileCopyResp FileCopyResp

	_, err := d.request("https://api.aliyundrive.com/adrive/v2/batch", http.MethodPost, func(req *resty.Request) {
		req.SetBody(fileCopyReq).SetHeader("X-Canary", "client=web,app=share,version=v2.3.1").
			SetResult(&fileCopyResp)
	})

	if err != nil {
		utils.Log.Errorf("failed to add cache file, file_id:  %s  文件:  %s", file.GetID(), file.GetName())
		return nil, err
	}

	respInfo, _ := utils.Json.MarshalToString(fileCopyResp)

	if fileCopyResp.Responses[0].Status > 300 {
		utils.Log.Errorf("failed to add cache file, file_id:  %s  文件:  %s, response: %s, request: %s", file.GetID(), file.GetName(), respInfo, fileCopyReq)
		return nil, err
	}
	utils.Log.Infof("add cache file successfully, file_id:  %s  文件:  %s", file.GetID(), file.GetName())

	utils.Log.Infof("response: %s", respInfo)

	// 新的file id
	newFileId := fileCopyResp.Responses[0].Body.FileID

	// 生成aliyun open的链接
	newFile := File{
		FileId:   newFileId,
		Name:     file.GetName(),
		DomainId: fileCopyResp.Responses[0].Body.DomainID,
		DriveId:  fileCopyResp.Responses[0].Body.DriveID,
	}

	if err != nil {
		return nil, err
	}

	// 获取直链
	newFileObj = fileToObj(newFile)

	CacheTempFile[file.GetID()] = newFileObj
	queue = append(queue, file.GetID())

	if len(CacheTempFile) > CacheConfig.MaxTempFileSize {
		deleteKey := queue[0]
		queue = queue[1:]
		deleteFile := CacheTempFile[deleteKey]
		delete(CacheTempFile, deleteKey)
		go func() {
			utils.Log.Infof("start to delete cache file, file_id:  %s  文件:  %s", file.GetID(), file.GetName())
			err := OpenAliyunDriver.(*aliyundrive_open.AliyundriveOpen).Remove(ctx, deleteFile)
			if err != nil {
				utils.Log.Errorf("failed to delete cache file, file_id:  %s  文件:  %s, error: %+v", file.GetID(), file.GetName(), err)
			}
		}()
	}

	return OpenAliyunDriver.Link(ctx, newFileObj, model.LinkArgs{})

	//
	//data := base.Json{
	//	"drive_id": d.DriveId,
	//	"file_id":  file.GetID(),
	//	// // Only ten minutes lifetime
	//	"expire_sec": 600,
	//	"share_id":   d.ShareId,
	//}
	//var resp ShareLinkResp
	//_, err = d.request("https://api.aliyundrive.com/v2/file/get_share_link_download_url", http.MethodPost, func(req *resty.Request) {
	//	req.SetBody(data).SetResult(&resp)
	//})
	//if err != nil {
	//	return nil, err
	//}
	//return &model.Link{
	//	Header: http.Header{
	//		"Referer": []string{"https://www.aliyundrive.com/"},
	//	},
	//	URL: resp.DownloadUrl,
	//}, nil
}

func (d *AliyundriveShare) Other(ctx context.Context, args model.OtherArgs) (interface{}, error) {
	var resp base.Json
	var url string
	data := base.Json{
		"share_id": d.ShareId,
		"file_id":  args.Obj.GetID(),
	}
	switch args.Method {
	case "doc_preview":
		url = "https://api.aliyundrive.com/v2/file/get_office_preview_url"
	case "video_preview":
		url = "https://api.aliyundrive.com/v2/file/get_video_preview_play_info"
		data["category"] = "live_transcoding"
	default:
		return nil, errs.NotSupport
	}
	_, err := d.request(url, http.MethodPost, func(req *resty.Request) {
		req.SetBody(data).SetResult(&resp)
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

var _ driver.Driver = (*AliyundriveShare)(nil)
