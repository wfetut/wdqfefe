import { useEffect, useState } from 'react';

import { getAccessToken, getHostName } from 'teleport/services/api';

import cfg from 'teleport/config';

import FileTransferClient from './fileTransferClient';

type Props = {
  clusterId: string;
  serverId: string;
  login: string;
};

export default function useFileTransferClient(props: Props) {
  const { clusterId, serverId, login } = props;
  const [fileTransferClient, setFileTransferClient] =
    useState<FileTransferClient | null>(null);

  useEffect(() => {
    // scp: 'wss://:fqdn/v1/webapi/sites/:clusterId/nodes/:serverId/scp?location=:location&filename=:filename&access_token=:token'
    const addr = cfg.api.scpWsAdr
      .replace(':fqdn', getHostName())
      .replace(':clusterId', clusterId)
      .replace(':serverId', serverId)
      .replace(':login', login)
      .replace(':token', getAccessToken());

    setFileTransferClient(new FileTransferClient(addr));
  }, [clusterId, serverId]);

  /* useEffect(() => { */
  /*   if (fileTransferClient) { */
  /*     fileTransferClient.init(); */
  /*   } */
  /* }, [fileTransferClient]); */

  return {
    fileTransferClient,
  };
}
