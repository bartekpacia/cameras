import cv2
import os
import uuid
import time
import argparse

def create_video_writer(img, idc, save_to_file=None):
    if save_to_file:
        full_path = save_to_file
    else:
        dir_path = os.path.join(os.getcwd(), "recordings")
        if not os.path.exists(dir_path):
            os.makedirs(dir_path)

        file_name = f"idc{idc}_{uuid.uuid4()}.mkv"
        full_path = os.path.join(dir_path, file_name)

    fourcc = cv2.VideoWriter_fourcc(*'X264')
    video_writer = cv2.VideoWriter(full_path, fourcc, 15, (img.shape[1], img.shape[0]), True)
    return video_writer

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--idc", type=int, default=1, help="camera number")
    parser.add_argument("--file", type=str, default="", help="video file to read from instead of RTSP")
    parser.add_argument("--tracking", action="store_true", help="whether to track human movement")
    parser.add_argument("--save-to-file", type=str, help="file to save the streamed video")
    args = parser.parse_args()

    if args.file and args.save_to_file:
        raise ValueError("--file and --save-to-file are mutually exclusive")

    user = os.getenv("DVR_USER")
    if not user:
        raise ValueError("DVR_USER is empty")

    password = os.getenv("DVR_PASSWORD")
    if not password:
        raise ValueError("DVR_PASSWORD is empty")

    address = os.getenv("DVR_ADDRESS")
    if not address:
        raise ValueError("DVR_ADDRESS is empty")

    port = os.getenv("DVR_PORT")
    if not port:
        raise ValueError("DVR_PORT is empty")

    if args.file:
        print("opening from video file")
        capture = cv2.VideoCapture(args.file)
    else:
        url = f"rtsp://{user}:{password}@{address}:{port}/mode=real&idc={args.idc}&ids=1"
        capture = cv2.VideoCapture(url)

    if not capture.isOpened():
        raise ValueError("failed to open video capture")

    ret, img = capture.read()
    if not ret:
        raise ValueError("failed to read a frame from video capture to matrix")

    video_writer = create_video_writer(img, args.idc, args.save_to_file)

    while True:
        ret, img = capture.read()
        if not ret:
            raise ValueError("failed to read a frame from video capture to matrix")

        cv2.imshow(f"video capture {args.idc}", img)
        cv2.waitKey(1)

        video_writer.write(img)

if __name__ == "__main__":
    main()
