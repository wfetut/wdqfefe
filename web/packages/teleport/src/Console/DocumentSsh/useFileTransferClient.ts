import { FileTransferListeners } from 'shared/components/FileTransfer';

import cfg from 'teleport/config';
import { getAccessToken, getHostName } from 'teleport/services/api';

import FileTransferClient from './fileTransferClient';

type DownloadProps = {
  location: string;
  clusterId: string;
  serverId: string;
  login: string;
  filename: string;
};

export default function useFileTransferClient() {
  const download = (
    props: DownloadProps,
    abortController: AbortController
  ): Promise<FileTransferListeners | undefined> => {
    const { location, login, filename, serverId, clusterId } = props;
    const addr = cfg.api.scpWsAdr
      .replace(':fqdn', getHostName())
      .replace(':clusterId', clusterId)
      .replace(':serverId', serverId)
      .replace(':login', login)
      .replace(':token', getAccessToken())
      .replace(':location', location)
      .replace(':filename', filename)
      .replace(':upload', 'upload');

    const ftc = new FileTransferClient(addr);
    ftc.download();

    return;
  };

  const upload = (
    props: DownloadProps,
    file: File,
    abortController: AbortController
  ): Promise<FileTransferListeners | undefined> => {
    const { location, login, filename, serverId, clusterId } = props;
    const addr = cfg.api.scpWsAdr
      .replace(':fqdn', getHostName())
      .replace(':clusterId', clusterId)
      .replace(':serverId', serverId)
      .replace(':login', login)
      .replace(':token', getAccessToken())
      .replace(':location', location)
      .replace(':filename', filename);

    /* const ftc = new FileTransferClient(addr); */
    /* ftc.init(); */

    return;
  };
  return {
    download,
    upload,
  };
}
