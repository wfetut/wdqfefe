import Logger from 'shared/libs/logger';

import { EventEmitterWebAuthnSender } from 'teleport/lib/EventEmitterWebAuthnSender';
import { WebauthnAssertionResponse } from 'teleport/services/auth';

export enum FileTransferClientEvent {
  WS_CLOSE = 'ws close',
}

export default class FileTransferClient extends EventEmitterWebAuthnSender {
  protected socket: WebSocket | undefined;
  private socketAddr: string;
  private logger = Logger.create('FileTransferClient');

  constructor(socketAddr: string) {
    super();
    this.socketAddr = socketAddr;
  }

  init() {
    this.socket = new WebSocket(this.socketAddr);
    this.socket.binaryType = 'arraybuffer';
    this.socket.onopen = () => {
      this.logger.info('websocket is open');
    };

    this.socket.onerror = err => console.log('error here', err);
    this.socket.onclose = () => {
      this.logger.info('websocket is closed');

      // Clean up all of our socket's listeners and the socket itself.
      this.socket.onopen = null;
      this.socket.onmessage = null;
      this.socket.onclose = null;
      this.socket = null;

      this.emit(FileTransferClientEvent.WS_CLOSE);
    };
  }

  sendWebAuthn(data: WebauthnAssertionResponse) {
    console.log('datadata', data);
  }
}
