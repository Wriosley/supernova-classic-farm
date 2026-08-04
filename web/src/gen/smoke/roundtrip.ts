import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import {
  Action,
  MessageKind,
  WsEnvelopeSchema,
} from "../classicfarm/v1/ws/ws_pb.js";

const playerId = 9_007_199_254_740_993n;
const requestId = "00000000-0000-4000-8000-000000000001";

const original = create(WsEnvelopeSchema, {
  protocolVersion: 1,
  messageKind: MessageKind.REQUEST,
  action: Action.GET_PLAYER_SNAPSHOT,
  requestId,
  targetPlayerId: playerId,
  payload: {
    case: "getPlayerSnapshotRequest",
    value: {},
  },
});

const decoded = fromBinary(WsEnvelopeSchema, toBinary(WsEnvelopeSchema, original));

if (
  decoded.targetPlayerId !== playerId ||
  decoded.requestId !== requestId ||
  decoded.payload.case !== "getPlayerSnapshotRequest"
) {
  throw new Error("generated TypeScript Protobuf round trip changed fields");
}

console.log("TypeScript Protobuf round-trip smoke test passed");
