package gatewayhttp

import (
	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/credentialacqhttp"
)

type AdminCredentialAcquisitionDeps = credentialacqhttp.AdminCredentialAcquisitionDeps

func MountAdminCredentialAcquisitionRoutes(r chi.Router, deps AdminCredentialAcquisitionDeps) {
	credentialacqhttp.MountAdminCredentialAcquisitionRoutes(r, deps)
}

func MountAdminCredentialAcquisitionHelperRoutes(r chi.Router, deps AdminCredentialAcquisitionDeps) {
	credentialacqhttp.MountAdminCredentialAcquisitionHelperRoutes(r, deps)
}
