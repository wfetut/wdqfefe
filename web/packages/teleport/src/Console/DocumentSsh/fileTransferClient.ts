import Logger from 'shared/libs/logger';

import { Protobuf } from 'teleport/lib/term/protobuf';
import {
  makeMfaAuthenticateChallenge,
  makeWebauthnAssertionResponse,
  WebauthnAssertionResponse,
} from 'teleport/services/auth';

export const MessageTypeEnum = {
  RAW: 'r',
  FILENAME: 'f',
  FILESIZE: 's',
  WEBAUTHN_CHALLENGE: 'n',
};

export default class FileTransferClient {
  protected socket: WebSocket | undefined;
  /* protected codec: Codec; */

  private _filename: string;
  private _socketAddr: string;
  private _logger = Logger.create('FileTransferClient');
  private _chunks = [];
  private _receivedSize = 0;
  _proto = new Protobuf();

  constructor(socketAddr: string) {
    this._socketAddr = socketAddr;
    /* this.codec = new Codec(); */
  }

  async download() {
    const file = await window.showSaveFilePicker();
    const writableStream = await file.createWritable();
    this.socket = new WebSocket(this._socketAddr);
    this.socket.binaryType = 'arraybuffer';
    this.socket.onopen = () => {
      this._logger.info('websocket is open');
    };
    /* const fileStream = new WritableStream<Uint8Array>({ */
    /*   write(chunk) { */
    /*     // Write each chunk of data to the file as it is received */
    /*     const writer = fileWriter.getWriter(); */
    /*     writer.write(chunk); */
    /*     writer.releaseLock(); */
    /*   }, */
    /* }); */
    /**/
    this.socket.onmessage = async (ev: MessageEvent) => {
      await writableStream.write(ev.data);
    };
    this.socket.onclose = async () => {
      await writableStream.close();
      this._logger.info('websocket is closed');
      /* this._logger.info('websocket is closed'); */
      /* this.saveOnDisk('masters.zip'); */
      /* this._chunks = []; */
    };
  }

  async processMessage(data: ArrayBuffer) {
    /* const uintArray = new Uint8Array(data); */
    console.log(data);
    /* const msg = this._proto.decodeFileTransfer(uintArray); */
    /* switch (msg.type) { */
    /*   case MessageTypeEnum.WEBAUTHN_CHALLENGE: */
    /*     this.authenticate(msg.payload); */
    /*     break; */
    /*   case MessageTypeEnum.RAW: */
    /*     this.prepareFileBuffer(msg.payload); */
    /*     break; */
    /*   default: */
    /*     throw Error(`unknown message type: ${msg.type}`); */
    /* } */
  }

  prepareFileBuffer(data) {
    this._receivedSize += data.length;
    this._chunks.push(data);
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

  authenticate(data) {
    const msg = this._proto.decode(data);
    const challenge = JSON.parse(msg.payload);
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
