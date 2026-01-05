from setuptools import setup, find_packages

setup(
    name="gap-image",
    version="1.2.02",
    description="Python wrapper for the GAP Image Codec",
    author="GAP Team",
    packages=find_packages(where="python"),
    package_dir={"": "python"},
    include_package_data=True,
    package_data={
        "pygap": ["bin/**/*"],
    },
    python_requires=">=3.6",
)
