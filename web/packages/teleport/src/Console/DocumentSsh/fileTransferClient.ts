import Logger from 'shared/libs/logger';

import Codec, {
  MessageType,
  FileType,
  SharedDirectoryErrCode,
  Severity,
} from 'teleport/lib/tdp/codec';
import {
  makeMfaAuthenticateChallenge,
  makeWebauthnAssertionResponse,
  MfaAuthenticateChallenge,
} from 'teleport/services/auth';

export default class FileTransferClient {
  protected socket: WebSocket | undefined;
  protected codec: Codec;

  private socketAddr: string;
  private logger = Logger.create('FileTransferClient');

  constructor(socketAddr: string) {
    /* super(); */
    this.socketAddr = socketAddr;
    this.codec = new Codec();
  }

  init() {
    this.socket = new WebSocket(this.socketAddr);
    this.socket.binaryType = 'arraybuffer';
    this.socket.onopen = () => {
      this.logger.info('websocket is open');
      /* this.emit(TdpClientEvent.WS_OPEN); */
    };

    this.socket.onmessage = async (ev: MessageEvent) => {
      await this.processMessage(ev.data as ArrayBuffer);
    };
    this.socket.onclose = () => {
      this.logger.info('websocket is closed');

      // Clean up all of our socket's listeners and the socket itself.
      /* this.socket.onopen = null; */
      /* this.socket.onmessage = null; */
      /* this.socket.onclose = null; */
      /* this.socket = null; */

      /* this.emit(TdpClientEvent.WS_CLOSE); */
    };
  }

  /* const onChallenge = challengeJson => { */
  /*   const challenge = JSON.parse(challengeJson); */
  /*   const publicKey = makeMfaAuthenticateChallenge(challenge).webauthnPublicKey; */
  /**/
  /*   setState({ */
  /*     ...state, */
  /*     requested: true, */
  /*     publicKey, */
  /*   }); */
  /* }; */
  async processMessage(message: ArrayBuffer) {
    console.log('message here: ', message);
    /* const mfaJson = this.codec.decodeMfaJson(message); */
    /* const publicKey = makeMfaAuthenticateChallenge( */
    /*   JSON.parse(mfaJson.jsonString) */
    /* ).webauthnPublicKey; */
    /**/
    /* this.authenticate(publicKey); */
  }

  authenticate(publicKey: PublicKeyCredentialRequestOptions) {
    if (!window.PublicKeyCredential) {
      const errorText =
        'This browser does not support WebAuthn required for hardware tokens, \
      please try the latest version of Chrome, Firefox or Safari.';
      console.log('errorText', errorText);

      return;
    }
    navigator.credentials.get({ publicKey }).then(res => {
      const credential = makeWebauthnAssertionResponse(res);
      console.log('credential', credential);
      const msg = this.codec.encodeMfaJson({
        mfaType: 'n',
        jsonString: JSON.stringify(credential),
      });
      this.socket.send(msg);
      /* this.send(msg); */
    });
  }
  /* navigator.credentials */
  /*   .get({ publicKey }) */
  /*   .then(res => { */
  /*     const credential = makeWebauthnAssertionResponse(res); */
  /*     emitterSender.sendWebAuthn(credential); */
  /**/
  /*     setState({ */
  /*       ...state, */
  /*       requested: false, */
  /*       errorText: '', */
  /*     }); */
  /*   }) */
  /*   .catch((err: Error) => { */
  /*     setState({ */
  /*       ...state, */
  /*       errorText: err.message, */
  /*     }); */
  /*   }); */
  /* handleMfaChallenge(buffer: ArrayBuffer) { */
  /*   try { */
  /*     const mfaJson = this.codec.decodeMfaJson(buffer); */
  /*     if (mfaJson.mfaType == 'n') { */
  /*       this.emit(TermEvent.WEBAUTHN_CHALLENGE, mfaJson.jsonString); */
  /*     } else { */
  /*       // mfaJson.mfaType === 'u', or else decodeMfaJson would have thrown an error. */
  /*       this.handleError( */
  /*         new Error( */
  /*           'Multifactor authentication is required for accessing this desktop, \ */
  /*     however the U2F API for hardware keys is not supported for desktop sessions. \ */
  /*     Please notify your system administrator to update cluster settings \ */
  /*     to use WebAuthn as the second factor protocol.' */
  /*         ), */
  /*         TdpClientEvent.CLIENT_ERROR */
  /*       ); */
  /*     } */
  /*   } catch (err) { */
  /*     this.handleError(err, TdpClientEvent.CLIENT_ERROR); */
  /*   } */
  /* } */
}
