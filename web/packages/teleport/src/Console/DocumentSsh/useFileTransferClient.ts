import { useEffect, useRef } from 'react';

import { getAccessToken, getHostName } from 'teleport/services/api';

import cfg from 'teleport/config';

type Props = {
  clusterId: string;
  serverId: string;
  login: string;
};

export default function useFileTransferClient(props: Props) {
  const { clusterId, serverId, login } = props;
  const ws = useRef<WebSocket | null>(null);

  useEffect(() => {
    // scp: 'wss://:fqdn/v1/webapi/sites/:clusterId/nodes/:serverId/:login/scp?location=:location&filename=:filename&access_token=:token'
    const addr = cfg.api.scpWsAdr
      .replace(':fqdn', getHostName())
      .replace(':clusterId', clusterId)
      .replace(':serverId', serverId)
      .replace(':login', login)
      .replace(':token', getAccessToken());

    ws.current = new WebSocket(addr);
    return () => {
      ws.current.close();
    };
  }, [clusterId, serverId]);

  const sendWebAuthn = () => {
    if (ws.current) {
      ws.current.send('MESSAGE');
    }
  };

  return {
    sendWebAuthn,
  };
}
