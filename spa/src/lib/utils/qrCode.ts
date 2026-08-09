import qrcode from 'qrcode-generator';

/**
 * Renders `text` to a QR code as a PNG data URL — client-side only, no
 * network calls. Used for invite links, which carry a secret in the URL
 * fragment; never route this through a third-party QR API.
 */
export function qrCodeDataURL(text: string, cellSize = 6, margin = 2): string {
  const qr = qrcode(0, 'M');
  qr.addData(text);
  qr.make();
  return qr.createDataURL(cellSize, margin);
}
