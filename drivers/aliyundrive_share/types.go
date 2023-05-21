package aliyundrive_share

import (
	"time"

	"github.com/alist-org/alist/v3/internal/model"
)

type ErrorResp struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ShareTokenResp struct {
	ShareToken string    `json:"share_token"`
	ExpireTime time.Time `json:"expire_time"`
	ExpiresIn  int       `json:"expires_in"`
}

type ListResp struct {
	Items             []File `json:"items"`
	NextMarker        string `json:"next_marker"`
	PunishedFileCount int    `json:"punished_file_count"`
}

type File struct {
	DriveId      string    `json:"drive_id"`
	DomainId     string    `json:"domain_id"`
	FileId       string    `json:"file_id"`
	ShareId      string    `json:"share_id"`
	Name         string    `json:"name"`
	Type         string    `json:"type"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	ParentFileId string    `json:"parent_file_id"`
	Size         int64     `json:"size"`
	Thumbnail    string    `json:"thumbnail"`
}

func fileToObj(f File) *model.ObjThumb {
	return &model.ObjThumb{
		Object: model.Object{
			ID:       f.FileId,
			Name:     f.Name,
			Size:     f.Size,
			Modified: f.UpdatedAt,
			IsFolder: f.Type == "folder",
		},
		Thumbnail: model.Thumbnail{Thumbnail: f.Thumbnail},
	}
}

type ShareLinkResp struct {
	DownloadUrl string `json:"download_url"`
	Url         string `json:"url"`
	Thumbnail   string `json:"thumbnail"`
}

type Request struct {
	Body struct {
		FileID         string `json:"file_id"`
		ShareID        string `json:"share_id"`
		AutoRename     bool   `json:"auto_rename"`
		ToParentFileID string `json:"to_parent_file_id"`
		ToDriveID      string `json:"to_drive_id"`
	} `json:"body"`
	Headers struct {
		ContentType string `json:"Content-Type"`
	} `json:"headers"`
	ID     string `json:"id"`
	Method string `json:"method"`
	URL    string `json:"url"`
}

type FileCopyReq struct {
	Requests []Request `json:"requests"`
	Resource string    `json:"resource"`
}

type FileCopyResp struct {
	Responses []struct {
		Body struct {
			DomainID string `json:"domain_id"`
			DriveID  string `json:"drive_id"`
			FileID   string `json:"file_id"`
		} `json:"body"`
		ID     string `json:"id"`
		Status int    `json:"status"`
	} `json:"responses"`
	DistributorCouponInfo struct {
		Title           string `json:"title"`
		Description     string `json:"description"`
		ButtonText      string `json:"buttonText"`
		ButtonSchemaURL string `json:"buttonSchemaUrl"`
		DisplayValidity string `json:"displayValidity"`
		MaxSaving       string `json:"maxSaving"`
		DisplayCurrency string `json:"displayCurrency"`
	} `json:"distributorCouponInfo"`
}

type Config struct {
	SharedRefreshToken string `json:"shared_refresh_token" required:"true"`
	SharedAccessToken  string `json:"-"`
	OpenRefreshToken   string `json:"open_refresh_token" required:"true"`
	OrderBy            string `json:"order_by" type:"select" options:"name,size,updated_at,created_at"`
	OrderDirection     string `json:"order_direction" type:"select" options:"ASC,DESC"`
	OauthTokenURL      string `json:"oauth_token_url" default:"https://api.nn.ci/alist/ali_open/token"`
	ClientID           string `json:"client_id" required:"false" help:"Keep it empty if you don't have one"`
	ClientSecret       string `json:"client_secret" required:"false" help:"Keep it empty if you don't have one"`
	RemoveWay          string `json:"remove_way" required:"true" type:"select" options:"trash,delete"`
	InternalUpload     bool   `json:"internal_upload" help:"If you are using Aliyun ECS is located in Beijing, you can turn it on to boost the upload speed"`
	TempFolderId       string `json:"temp_folder_id" help:"temp dir for cache shared aliyun"`
}
