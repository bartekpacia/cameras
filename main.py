import cv2
import sys
import os

os.environ["OPENCV_FFMPEG_CAPTURE_OPTIONS"] = "rtsp_transport;udp"

username = sys.argv[1]
password = sys.argv[2]
idc = sys.argv[3]

url = f"rtsp://{username}:{password}@192.168.1.100:554/mode=real&idc={idc}&ids=1"

capture = cv2.VideoCapture(url, cv2.CAP_FFMPEG)

while 1:
    ret, frame = capture.read()
    if ret == False:
        print("Frame is empty")
        break
    else:
        cv2.imshow("VIDEO", frame)
        cv2.waitKey(1)

capture.release()
cv2.destroyAllWindows()
