package cd

import "github.com/suma/suma/server/internal/deployment"

var validateComposeSources = deployment.ValidateComposeSources
var readDeploymentFile = deployment.ReadDeploymentFile

const maxDeploymentFileSize = 2 << 20
