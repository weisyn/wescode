/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

import { registerMainProcessRemoteService } from '../../../../platform/ipc/electron-sandbox/services.js';
import { IWescodeBackendService, ipcWescodeChannelName } from '../../../../platform/wescode/common/wescode.js';

registerMainProcessRemoteService(IWescodeBackendService, ipcWescodeChannelName);
