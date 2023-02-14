import Logger from 'shared/libs/logger';

import { Protobuf, MessageTypeEnum } from 'teleport/lib/term/protobuf';
import {
  makeMfaAuthenticateChallenge,
  makeWebauthnAssertionResponse,
  WebauthnAssertionResponse,
} from 'teleport/services/auth';

export default class FileTransferClient {
  protected socket: WebSocket | undefined;
  /* protected codec: Codec; */

  private _filename: string;
  private _socketAddr: string;
  private _logger = Logger.create('FileTransferClient');
  private _chunks = [];
  _proto = new Protobuf();

  constructor(socketAddr: string) {
    this._socketAddr = socketAddr;
    /* this.codec = new Codec(); */
  }

  init() {
    this.socket = new WebSocket(this._socketAddr);
    this.socket.binaryType = 'arraybuffer';
    this.socket.onopen = () => {
      this._logger.info('websocket is open');
    };

    this.socket.onmessage = async (ev: MessageEvent) => {
      await this.processMessage(ev.data as ArrayBuffer);
    };
    this.socket.onclose = () => {
      this._logger.info('websocket is closed');
      this.saveOnDisk('masters.zip');
    };
  }

  async processMessage(data: ArrayBuffer) {
    this.prepareFileBuffer(data);
    /* const uintArray = new Uint8Array(data); */
    /* const msg = this._proto.decode(uintArray); */
    /* switch (msg.type) { */
    /*   case MessageTypeEnum.WEBAUTHN_CHALLENGE: */
    /*     this.authenticate(msg.payload); */
    /*     break; */
    /*   case MessageTypeEnum.RAW: */
    /*     this.prepareFileBuffer(uintArray); */
    /*     break; */
    /*   default: */
    /*     throw Error(`unknown message type: ${msg.type}`); */
    /* } */
  }

  prepareFileBuffer(data) {
    let newData = new Uint8Array(data);
    this._chunks.push(newData);
  }

  saveOnDisk(fileName: string): void {
    let blob = new Blob(this._chunks, { type: 'application/octet-stream' });
    const a = document.createElement('a');
    a.href = window.URL.createObjectURL(blob);
    a.download = fileName;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
  }

  send(data) {
    if (!this.socket || !data) {
      return;
    }

    const msg = this._proto.encodeRawMessage(data);
    const bytearray = new Uint8Array(msg);
    this.socket.send(bytearray.buffer);
  }

  sendWebAuthn(data: WebauthnAssertionResponse) {
    this.send(JSON.stringify(data));
  }

  authenticate(challengeJson) {
    const challenge = JSON.parse(challengeJson);
    const publicKey = makeMfaAuthenticateChallenge(challenge).webauthnPublicKey;
    if (!window.PublicKeyCredential) {
      const errorText =
        'This browser does not support WebAuthn required for hardware tokens, \
      please try the latest version of Chrome, Firefox or Safari.';
      console.log('errorText', errorText);

      return;
    }
    navigator.credentials.get({ publicKey }).then(res => {
      const credential = makeWebauthnAssertionResponse(res);
      this.sendWebAuthn(credential);
    });
  }
}
