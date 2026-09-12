import landing from './landing'
import common from './common'
import dashboard from './dashboard'
import channelMonitorV2 from './channelMonitorV2'
import batchImage from './batchImage'
import admin from './admin'
import misc from './misc'
import fork from './fork'
import { deepMergeMessages } from '../deepMergeMessages'

// fork 是本仓库（二开）的键覆盖层：必须先展开上游模块，再按命名空间深合并，
// 否则 fork 顶层同名的 `admin` 会整体替换上游的 admin 命名空间。
export default deepMergeMessages(
  {
    ...landing,
    ...common,
    ...dashboard,
    ...channelMonitorV2,
    ...batchImage,
    admin,
    ...misc,
  },
  fork
)
